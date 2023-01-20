// Copyright (C) 2020 Storj Labs, Inc.
// See LICENSE for copying information.

package linksharing

import (
	"context"
	"net/url"

	"github.com/oschwald/maxminddb-golang"
	"github.com/spacemonkeygo/monkit/v3"
	"github.com/spacemonkeygo/monkit/v3/http"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/gateway-mt/pkg/authclient"
	"storj.io/gateway-mt/pkg/httpserver"
	"storj.io/gateway-mt/pkg/linksharing/objectmap"
	"storj.io/gateway-mt/pkg/linksharing/sharing"
	"storj.io/gateway-mt/pkg/server/middleware"
)

var mon = monkit.Package()

// Config contains configurable values for sno registration Peer.
type Config struct {
	Server  httpserver.Config
	Handler sharing.Config

	// Maxmind geolocation database path.
	GeoLocationDB string
}

// Peer is the representation of a Linksharing service itself.
//
// architecture: Peer
type Peer struct {
	Log        *zap.Logger
	Mapper     *objectmap.IPDB
	Server     *httpserver.Server
	TXTRecords *sharing.TXTRecords
}

// New is a constructor for Linksharing Peer.
func New(log *zap.Logger, config Config) (_ *Peer, err error) {
	dns, err := sharing.NewDNSClient(config.Handler.DNSServer)
	if err != nil {
		return nil, err
	}
	authClient := authclient.New(config.Handler.AuthServiceConfig)
	txtRecords := sharing.NewTXTRecords(config.Handler.TXTRecordTTL, dns, authClient)
	if err != nil {
		return nil, err
	}

	peer := &Peer{
		Log:        log,
		TXTRecords: txtRecords,
	}

	if config.GeoLocationDB != "" {
		reader, err := maxminddb.Open(config.GeoLocationDB)
		if err != nil {
			return nil, errs.New("unable to open geo location db: %w", err)
		}
		peer.Mapper = objectmap.NewIPDB(reader)
	}

	handle, err := sharing.NewHandler(log, peer.Mapper, txtRecords, authClient, config.Handler)
	if err != nil {
		return nil, errs.New("unable to create handler: %w", err)
	}

	handleWithTracing := http.TraceHandler(handle, mon)
	instrumentedHandle := middleware.Metrics("linksharing", handleWithTracing)

	var decisionFunc httpserver.CertMagicOnDemandDecisionFunc
	if config.Server.TLSConfig != nil && config.Server.TLSConfig.CertMagic {
		decisionFunc, err = customDomainsOverTLSDecisionFunc(config.Server.TLSConfig, txtRecords)
		if err != nil {
			return nil, errs.New("unable to get decision func for Custom Domains@TLS feature: %w", err)
		}
	}

	peer.Server, err = httpserver.New(log, instrumentedHandle, decisionFunc, config.Server)
	if err != nil {
		return nil, errs.New("unable to create httpserver: %w", err)
	}

	return peer, nil
}

func customDomainsOverTLSDecisionFunc(tlsConfig *httpserver.TLSConfig, txtRecords *sharing.TXTRecords) (httpserver.CertMagicOnDemandDecisionFunc, error) {
	bases := make([]*url.URL, 0, len(tlsConfig.PublicURLs))
	for _, base := range tlsConfig.PublicURLs {
		parsed, err := url.Parse(base)
		if err != nil {
			return nil, errs.New("invalid public URL %q: %v", base, err)
		}
		bases = append(bases, parsed)
	}

	tqs, err := sharing.NewTierQueryingService(tlsConfig.TierServiceIdentityPath, tlsConfig.TierCacheExpiration, tlsConfig.TierCacheCapacity)
	if err != nil {
		return nil, errs.New("unable to create tier querying service: %w", err)
	}

	return func(name string) error {
		// allow configured urls
		for _, url := range bases {
			if name == url.Host {
				return nil
			}
		}
		// validate dns txt records for everyone else
		result, err := txtRecords.FetchAccessForHostNoAccessGrant(tlsConfig.Ctx, name, "")
		if err != nil {
			return err
		}
		if !result.TLS {
			return errs.New("tls not enabled")
		}
		// validate requester is a paying customer
		for _, allowed := range tlsConfig.SkipPaidTierAllowlist {
			if allowed == "*" || name == allowed {
				// skip paid tier query
				return nil
			}
		}
		paidTier, err := tqs.Do(tlsConfig.Ctx, result.Access, name)
		if err != nil {
			return err
		}
		if !paidTier {
			return errs.New("not paid tier")
		}
		// request cert
		return nil
	}, nil
}

// Run starts the server.
func (peer *Peer) Run(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)

	return peer.Server.Run(ctx)
}

// Close shuts down the server and all underlying resources.
func (peer *Peer) Close() error {
	var errlist errs.Group

	if peer.Server != nil {
		errlist.Add(peer.Server.Shutdown())
	}

	if peer.Mapper != nil {
		errlist.Add(peer.Mapper.Close())
	}

	return errlist.Err()
}
