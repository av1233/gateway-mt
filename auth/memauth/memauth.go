// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package memauth

import (
	"context"
	"sync"

	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"

	"storj.io/stargate/auth"
)

var mon = monkit.Package()

// KV is a key/value store backed by an in memory map.
type KV struct {
	mu      sync.Mutex
	entries map[auth.KeyHash]*auth.Record
}

// New constructs a KV.
func New() *KV {
	return &KV{
		entries: make(map[auth.KeyHash]*auth.Record),
	}
}

// Put stores the record in the key/value store.
// It is an error if the key already exists.
func (d *KV) Put(ctx context.Context, keyHash auth.KeyHash, record *auth.Record) (err error) {
	defer mon.Task()(&ctx)(&err)

	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.entries[keyHash]; ok {
		return errs.New("record already exists")
	}

	d.entries[keyHash] = record
	return nil
}

// Get retreives the record from the key/value store.
// It returns nil if the key does not exist.
func (d *KV) Get(ctx context.Context, keyHash auth.KeyHash) (record *auth.Record, err error) {
	defer mon.Task()(&ctx)(&err)

	d.mu.Lock()
	defer d.mu.Unlock()

	return d.entries[keyHash], nil
}