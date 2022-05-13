// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package badgerauth_test

import (
	"testing"
	"time"

	badger "github.com/outcaste-io/badger/v3"
	"github.com/stretchr/testify/require"

	"storj.io/common/testcontext"
	"storj.io/gateway-mt/pkg/auth/authdb"
	"storj.io/gateway-mt/pkg/auth/badgerauth"
	"storj.io/gateway-mt/pkg/auth/badgerauth/badgerauthtest"
	"storj.io/gateway-mt/pkg/auth/badgerauth/pb"
)

func TestNode_Replicate_EmptyRequestResponse(t *testing.T) {
	badgerauthtest.RunSingleNode(t, badgerauth.Config{
		ID:               badgerauth.NodeID{'t', 'e', 's', 't'},
		ReplicationLimit: 123,
	}, func(ctx *testcontext.Context, t *testing.T, node *badgerauth.Node) {
		// empty request/response
		badgerauthtest.Replicate{
			Request: &pb.ReplicationRequest{},
			Result:  &pb.ReplicationResponse{},
		}.Check(ctx, t, node)

		badgerauthtest.Replicate{
			Request: &pb.ReplicationRequest{
				Entries: []*pb.ReplicationRequestEntry{
					{
						NodeId: []byte{'t', 'e', 's', 't'},
						Clock:  0,
					},
					{
						NodeId: []byte{'t', 's', 'e', 't'},
						Clock:  1,
					},
				},
			},
			Result: &pb.ReplicationResponse{},
		}.Check(ctx, t, node)

		badgerauthtest.Put{
			KeyHash: authdb.KeyHash{'k', 'h'},
			Record:  &authdb.Record{},
		}.Check(ctx, t, node.UnderlyingDB())

		badgerauthtest.Replicate{
			Request: &pb.ReplicationRequest{
				Entries: []*pb.ReplicationRequestEntry{
					{
						NodeId: []byte{'t', 'e', 's', 't'},
						Clock:  1,
					},
					{
						NodeId: []byte{'t', 's', 'e', 't'},
						Clock:  2,
					},
				},
			},
			Result: &pb.ReplicationResponse{},
		}.Check(ctx, t, node)
	})
}

func TestNode_Replicate_OverlappingNodeIDs(t *testing.T) {
	badgerauthtest.RunSingleNode(t, badgerauth.Config{
		ID:               badgerauth.NodeID{'a', 'a'},
		ReplicationLimit: 123,
	}, func(ctx *testcontext.Context, t *testing.T, node *badgerauth.Node) {
		badgerauthtest.Put{
			KeyHash: authdb.KeyHash{'k', 'h'},
			Record:  &authdb.Record{},
		}.Check(ctx, t, node.UnderlyingDB())

		badgerauthtest.Replicate{
			Request: &pb.ReplicationRequest{
				Entries: []*pb.ReplicationRequestEntry{
					{
						NodeId: []byte{'a'},
						Clock:  0,
					},
					{
						NodeId: []byte{'a', 'a'},
						Clock:  1,
					},
				},
			},
			Result: &pb.ReplicationResponse{},
		}.Check(ctx, t, node)
	})
}

func TestNode_Replicate_Basic(t *testing.T) {
	badgerauthtest.RunSingleNode(t, badgerauth.Config{
		ID:               badgerauth.NodeID{'a'},
		ReplicationLimit: 25,
	}, func(ctx *testcontext.Context, t *testing.T, node *badgerauth.Node) {
		// test's outline:
		//  1. node A knows about nodes A B C D
		//  2. another node requests information about A B C D E from A
		//
		// test's plan:
		//  A: has record(s) 0-50    | request for clock > 25  | returns 25 records
		//  B: has record(s) 51      | request for clock > 0   | returns 1  records
		//  C: has record(s) 52-100  | request for clock > 12  | returns 25 records (hits the limit)
		//  D: has record(s) 100-255 | request for clock > 155 | returns 0  records
		//  E: (A doesn't know about E)
		db := node.UnderlyingDB()

		var expectedReplicationResponseEntries []*pb.ReplicationResponseEntry

		for i := 0; i < 50; i++ {
			r := &authdb.Record{
				SatelliteAddress:     "t",
				MacaroonHead:         []byte{'e'},
				EncryptedSecretKey:   []byte{'s'},
				EncryptedAccessGrant: []byte{'t'},
				Public:               false,
			}

			kh := authdb.KeyHash{byte(i)}
			now := time.Now()

			badgerauthtest.PutAtTime{
				KeyHash: authdb.KeyHash{byte(i)},
				Record:  r,
				Time:    now,
			}.Check(ctx, t, db)

			if i >= 25 {
				expectedReplicationResponseEntries = append(expectedReplicationResponseEntries, &pb.ReplicationResponseEntry{
					NodeId:            badgerauth.NodeID{'a'}.Bytes(),
					EncryptionKeyHash: kh[:],
					Record: &pb.Record{
						CreatedAtUnix:        now.Unix(),
						Public:               false,
						SatelliteAddress:     r.SatelliteAddress,
						MacaroonHead:         r.MacaroonHead,
						EncryptedSecretKey:   r.EncryptedSecretKey,
						EncryptedAccessGrant: r.EncryptedAccessGrant,
						State:                pb.Record_CREATED,
					},
				})
			}
		}

		require.NoError(t, db.UnderlyingDB().Update(func(txn *badger.Txn) error {
			for i := 52; i < 100; i++ {
				id := badgerauth.NodeID{'c'}
				kh := authdb.KeyHash{byte(i)}
				now := time.Now()
				record := &pb.Record{
					CreatedAtUnix:        now.Unix(),
					Public:               true,
					SatelliteAddress:     "x",
					MacaroonHead:         []byte{'y'},
					ExpiresAtUnix:        now.Add(24 * time.Hour).Unix(),
					EncryptedSecretKey:   []byte{'z'},
					EncryptedAccessGrant: []byte{'?'},
					State:                pb.Record_CREATED,
				}

				if err := badgerauth.InsertRecord(txn, id, kh, record); err != nil {
					return err
				}

				if i >= 52+12 && i < 52+12+25 {
					expectedReplicationResponseEntries = append(expectedReplicationResponseEntries, &pb.ReplicationResponseEntry{
						NodeId:            id.Bytes(),
						EncryptionKeyHash: kh[:],
						Record:            record,
					})
				}
			}

			for i := 100; i < 255; i++ {
				if err := badgerauth.InsertRecord(txn, badgerauth.NodeID{'d'}, authdb.KeyHash{byte(i)}, &pb.Record{}); err != nil {
					return err
				}
			}

			id := badgerauth.NodeID{'b'}
			kh := authdb.KeyHash{51}
			now := time.Now()
			record := &pb.Record{
				CreatedAtUnix:        now.Unix(),
				Public:               true,
				SatelliteAddress:     "a",
				MacaroonHead:         []byte{'b'},
				ExpiresAtUnix:        now.Add(24 * time.Hour).Unix(),
				EncryptedSecretKey:   []byte{'c'},
				EncryptedAccessGrant: []byte{'!'},
				State:                pb.Record_CREATED,
			}

			if err := badgerauth.InsertRecord(txn, id, kh, record); err != nil {
				return err
			}

			expectedReplicationResponseEntries = append(expectedReplicationResponseEntries, &pb.ReplicationResponseEntry{
				NodeId:            id.Bytes(),
				EncryptionKeyHash: kh[:],
				Record:            record,
			})

			return nil
		}))

		// The request below should produce an empty response:
		badgerauthtest.Replicate{
			Request: &pb.ReplicationRequest{
				Entries: []*pb.ReplicationRequestEntry{
					{
						NodeId: []byte{'a'},
						Clock:  50,
					},
					{
						NodeId: []byte{'b'},
						Clock:  1,
					},
					// Let's skip C.
					{
						NodeId: []byte{'d'},
						Clock:  155,
					},
				},
			},
			Result: &pb.ReplicationResponse{},
		}.Check(ctx, t, node)
		// Real request:
		badgerauthtest.Replicate{
			Request: &pb.ReplicationRequest{
				Entries: []*pb.ReplicationRequestEntry{
					{
						NodeId: []byte{'a'},
						Clock:  25,
					},
					{
						NodeId: []byte{'c'},
						Clock:  12,
					},
					{
						NodeId: []byte{'b'},
						Clock:  0,
					},
					{
						NodeId: []byte{'d'},
						Clock:  155,
					},
					{
						NodeId: []byte{'e'},
						Clock:  10000,
					},
				},
			},
			Result: &pb.ReplicationResponse{
				Entries: expectedReplicationResponseEntries,
			},
		}.Check(ctx, t, node)
	})
}
