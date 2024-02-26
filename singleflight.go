// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// fork from https://github.com/golang/go/blob/master/src/internal/singleflight/singleflight.go
// with generics and context support

// Package singleflight provides a duplicate function call suppression
// mechanism.
package singleflight

import (
	"context"
	"sync"
)

// doFunc is the function to be executed by Do and DoChan.
type doFunc[V any] func(context.Context) (V, error)

// call is an in-flight or completed singleflight.Do call
type call[V any] struct {
	wg sync.WaitGroup

	// These fields are written once before the WaitGroup is done
	// and are only read after the WaitGroup is done.
	val V
	err error

	// These fields are read and written with the singleflight
	// mutex held before the WaitGroup is done, and are read but
	// not written after the WaitGroup is done.
	dups  int
	chans []chan<- Result[V]
}

// Group represents a class of work and forms a namespace in
// which units of work can be executed with duplicate suppression.
type Group[K comparable, V any] struct {
	mu sync.Mutex     // protects m
	m  map[K]*call[V] // lazily initialized
}

// Result holds the results of Do, so they can be passed
// on a channel.
type Result[V any] struct {
	Val    V
	Err    error
	Shared bool
}

// Do executes and returns the results of the given function, making
// sure that only one execution is in-flight for a given key at a
// time. If a duplicate comes in, the duplicate caller waits for the
// original to complete and receives the same results.
// The return value shared indicates whether v was given to multiple callers.
// Context cancellation should be handled inside the function passed to `Do`,
// because singleflight does not interrupt the function execution if the context is canceled.
func (g *Group[K, V]) Do(ctx context.Context, key K, fn doFunc[V]) (v V, shared bool, err error) { // nolint: revive
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[K]*call[V])
	}
	if c, ok := g.m[key]; ok {
		c.dups++
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, true, c.err
	}
	c := new(call[V])
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	g.doCall(ctx, c, key, fn)
	return c.val, c.dups > 0, c.err
}

// DoChan is like Do but returns a channel that will receive the
// results when they are ready.
func (g *Group[K, V]) DoChan(ctx context.Context, key K, fn doFunc[V]) <-chan Result[V] {
	ch := make(chan Result[V], 1)
	g.mu.Lock()
	if g.m == nil {
		g.m = make(map[K]*call[V])
	}
	if c, ok := g.m[key]; ok {
		c.dups++
		c.chans = append(c.chans, ch)
		g.mu.Unlock()
		return ch
	}
	c := &call[V]{chans: []chan<- Result[V]{ch}}
	c.wg.Add(1)
	g.m[key] = c
	g.mu.Unlock()

	go g.doCall(ctx, c, key, fn)

	return ch
}

// doCall handles the single call for a key.
func (g *Group[K, V]) doCall(ctx context.Context, c *call[V], key K, fn doFunc[V]) {
	c.val, c.err = fn(ctx)

	g.mu.Lock()
	c.wg.Done()
	if g.m[key] == c {
		delete(g.m, key)
	}
	for _, ch := range c.chans {
		ch <- Result[V]{c.val, c.err, c.dups > 0}
	}
	g.mu.Unlock()
}

// ForgetUnshared tells the singleflight to forget about a key if it is not
// shared with any other goroutines. Future calls to Do for a forgotten key
// will call the function rather than waiting for an earlier call to complete.
// Returns whether the key was forgotten or unknown--that is, whether no
// other goroutines are waiting for the result.
func (g *Group[K, V]) ForgetUnshared(key K) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.m[key]
	if !ok {
		return true
	}
	if c.dups == 0 {
		delete(g.m, key)
		return true
	}
	return false
}
