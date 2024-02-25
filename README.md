[![Go Reference](https://pkg.go.dev/badge/github.com/n-r-w/singleflight.svg)](https://pkg.go.dev/github.com/n-r-w/singleflight)
[![Go Coverage](https://github.com/n-r-w/singleflight/wiki/coverage.svg)](https://raw.githack.com/wiki/n-r-w/singleflight/coverage.html)
![CI Status](https://github.com/n-r-w/singleflight/actions/workflows/go.yml/badge.svg)
[![Stability](http://badges.github.io/stability-badges/dist/stable.svg)](http://github.com/badges/stability-badges)
[![Go Report](https://goreportcard.com/badge/github.com/n-r-w/singleflight)](https://goreportcard.com/badge/github.com/n-r-w/singleflight)

# singleflight

Fork from `golang.org/x/sync/singleflight` with generics

## Usage

Singleflight is a concurrency method to prevent duplicate work from being executed due to multiple calls for the same resource.

```go
package main

import (
    "log"
    "time"

    "github.com/n-r-w/singleflight"
    "golang.org/x/sync/errgroup"
)

func main() {
    var g singleflight.Group[int, string]

    var errGroup errgroup.Group
    const key = 1

    // 10 goroutines are trying to get the value for the same key,
    // but only one of them will call the function and the others will wait for the result.
    for i := 0; i < 10; i++ {
        iCopy := i
        errGroup.Go(func() error {
            _, err, _ := g.Do(key, func() (string, error) {
                log.Println("called for", iCopy)
                time.Sleep(1 * time.Second)
                return "Hello, world!", nil
            })
            return err
        })
    }

    if err := errGroup.Wait(); err != nil {
        log.Println(err)
    }
}
```
