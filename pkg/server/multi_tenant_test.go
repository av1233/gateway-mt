// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package server

import (
	"context"
	"errors"
	"testing"

	miniogo "github.com/minio/minio-go/v7"
	minio "github.com/minio/minio/cmd"
	"github.com/minio/minio/cmd/logger"
	"github.com/stretchr/testify/require"

	"storj.io/gateway-mt/pkg/server/gwlog"
	"storj.io/gateway/miniogw"
	"storj.io/uplink"
)

func TestGetUserAgent(t *testing.T) {
	// ignore bad user agents
	reqInfo := logger.ReqInfo{UserAgent: "Test/1.0 S3 Browser 9.5.5 https://s3browser.com"}
	ctx := logger.SetReqInfo(context.Background(), &reqInfo)
	results := getUserAgent(ctx)
	require.Equal(t, "Gateway-MT/v0.0.0", results)
	// preserve good user agents
	reqInfo = logger.ReqInfo{UserAgent: "Test/1.0 S3-Browser/9.5.5 (https://s3browser.com)"}
	ctx = logger.SetReqInfo(context.Background(), &reqInfo)
	results = getUserAgent(ctx)
	require.Equal(t, "Test/1.0 S3-Browser/9.5.5 (https://s3browser.com) Gateway-MT/v0.0.0", results)
}

func TestMinioError(t *testing.T) {
	tests := []struct {
		input    error
		expected bool
	}{
		{errors.New("some error"), false},
		{uplink.ErrBucketNameInvalid, false},
		{miniogo.ErrorResponse{Message: "oops"}, true},
		{miniogw.ErrProjectUsageLimit, true},
		{miniogw.ErrSlowDown, true},
		{minio.BucketNotEmpty{}, true},
	}
	for i, tc := range tests {
		require.Equal(t, tc.expected, minioError(tc.input), i)
	}
}

func TestLogUnexpectedErrorsOnly(t *testing.T) {
	tests := []struct {
		input    error
		expected string
	}{
		{context.Canceled, ""},
		{minio.BucketNotEmpty{}, ""},
		{miniogo.ErrorResponse{Message: "oops"}, ""},
		{miniogw.ErrProjectUsageLimit, ""},
		{miniogw.ErrSlowDown, ""},
		{uplink.ErrBucketNameInvalid, uplink.ErrBucketNameInvalid.Error()},
		{errors.New("unexpected error"), "unexpected error"},
	}
	for i, tc := range tests {
		log := gwlog.New()
		ctx := log.WithContext(context.Background())
		require.Error(t, (&multiTenancyLayer{minio.GatewayUnsupported{}, nil, nil, uplink.Config{}, false}).log(ctx, tc.input))
		require.Equal(t, tc.expected, log.TagValue("error"), i)
	}
}

func TestLogAllErrors(t *testing.T) {
	tests := []struct {
		input    error
		expected string
	}{
		{context.Canceled, context.Canceled.Error()},
		{minio.BucketNotEmpty{}, minio.BucketNotEmpty{}.Error()},
		{uplink.ErrBucketNameInvalid, uplink.ErrBucketNameInvalid.Error()},
		{errors.New("unexpected error"), "unexpected error"},
	}
	for i, tc := range tests {
		log := gwlog.New()
		ctx := log.WithContext(context.Background())
		require.Error(t, (&multiTenancyLayer{minio.GatewayUnsupported{}, nil, nil, uplink.Config{}, true}).log(ctx, tc.input))
		require.Equal(t, tc.expected, log.TagValue("error"), i)
	}
}