// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// fork from https://github.com/golang/go/blob/master/src/internal/singleflight/singleflight_test.go
// with generics

// nolint: goconst
package singleflight

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDo(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	var g Group[string, string]
	v, _, err := g.Do(ctx, "key", func(ctxFunc context.Context) (string, error) {
		if ctxFunc != ctx {
			t.Error("wrong context in Do func")
		}
		return "bar", nil
	})
	if got, want := fmt.Sprintf("%v (%T)", v, v), "bar (string)"; got != want {
		t.Errorf("Do = %v; want %v", got, want)
	}
	if err != nil {
		t.Errorf("Do error = %v", err)
	}
}

func TestDoErr(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	var g Group[string, *string]
	someErr := errors.New("some error")
	v, _, err := g.Do(ctx, "key", func(ctxFunc context.Context) (*string, error) {
		if ctxFunc != ctx {
			t.Error("wrong context in Do func")
		}
		return nil, someErr
	})
	if err != someErr {
		t.Errorf("Do error = %v; want someErr %v", err, someErr)
	}
	if v != nil {
		t.Errorf("unexpected non-nil value %#v", v)
	}
}

func TestDoDupSuppress(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	var g Group[string, string]
	var wg1, wg2 sync.WaitGroup
	c := make(chan string, 1)
	var calls atomic.Int32
	fn := func(ctxFunc context.Context) (string, error) {
		if ctxFunc != ctx {
			t.Error("wrong context in Do func")
		}

		if calls.Add(1) == 1 {
			// First invocation.
			wg1.Done()
		}
		v := <-c
		c <- v // pump; make available for any future calls

		time.Sleep(10 * time.Millisecond) // let more goroutines enter Do

		return v, nil
	}

	const n = 10
	wg1.Add(1)
	for i := 0; i < n; i++ {
		wg1.Add(1)
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			wg1.Done()
			v, _, err := g.Do(ctx, "key", fn)
			if err != nil {
				t.Errorf("Do error: %v", err)
				return
			}
			if v != "bar" {
				t.Errorf("Do = %T %v; want %q", v, v, "bar")
			}
		}()
	}
	wg1.Wait()
	// At least one goroutine is in fn now and all of them have at
	// least reached the line before the Do.
	c <- "bar"
	wg2.Wait()
	if got := calls.Load(); got <= 0 || got >= n {
		t.Errorf("number of calls = %d; want over 0 and less than %d", got, n)
	}
}

func TestForgetUnshared(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	var g Group[string, int]

	var firstStarted, firstFinished sync.WaitGroup

	firstStarted.Add(1)
	firstFinished.Add(1)

	key := "key"
	firstCh := make(chan struct{})
	go func() {
		_, _, _ = g.Do(ctx, key, func(ctxFunc context.Context) (i int, e error) {
			if ctxFunc != ctx {
				t.Error("wrong context in Do func")
			}

			firstStarted.Done()
			<-firstCh
			return
		})
		firstFinished.Done()
	}()

	firstStarted.Wait()
	g.ForgetUnshared(key) // from this point no two function using same key should be executed concurrently

	secondCh := make(chan struct{})
	go func() {
		_, _, _ = g.Do(ctx, key, func(ctxFunc context.Context) (i int, e error) {
			if ctxFunc != ctx {
				t.Error("wrong context in Do func")
			}

			// Notify that we started
			secondCh <- struct{}{}
			<-secondCh
			return 2, nil
		})
	}()

	<-secondCh

	resultCh := g.DoChan(ctx, key, func(context.Context) (i int, e error) {
		panic("third must not be started")
	})

	if g.ForgetUnshared(key) {
		t.Errorf("Before first goroutine finished, key %q is shared, should return false", key)
	}

	close(firstCh)
	firstFinished.Wait()

	if g.ForgetUnshared(key) {
		t.Errorf("After first goroutine finished, key %q is still shared, should return false", key)
	}

	secondCh <- struct{}{}

	if result := <-resultCh; result.Val != 2 {
		t.Errorf("We should receive result produced by second call, expected: 2, got %d", result.Val)
	}
}

func TestDoAndForgetUnsharedRace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	var g Group[string, int64]
	key := "key"
	d := time.Millisecond
	for {
		var calls, shared atomic.Int64
		const n = 1000
		var wg sync.WaitGroup
		wg.Add(n)
		for i := 0; i < n; i++ {
			go func() {
				_, _, _ = g.Do(ctx, key, func(ctxFunc context.Context) (int64, error) {
					if ctxFunc != ctx {
						t.Error("wrong context in Do func")
					}

					time.Sleep(d)
					return calls.Add(1), nil
				})
				if !g.ForgetUnshared(key) {
					shared.Add(1)
				}
				wg.Done()
			}()
		}
		wg.Wait()

		if calls.Load() != 1 {
			// The goroutines didn't park in g.Do in time,
			// so the key was re-added and may have been shared after the call.
			// Try again with more time to park.
			d *= 2
			continue
		}

		// All of the Do calls ended up sharing the first
		// invocation, so the key should have been unused
		// (and therefore unshared) when they returned.
		if shared.Load() > 0 {
			t.Errorf("after a single shared Do, ForgetUnshared returned false %d times", shared.Load())
		}
		break
	}
}
