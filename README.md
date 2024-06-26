# goqite

<img src="docs/logo.png" alt="Logo" width="300" align="right">

[![GoDoc](https://pkg.go.dev/badge/github.com/maragudk/goqite)](https://pkg.go.dev/github.com/maragudk/goqite)
[![Go](https://github.com/maragudk/goqite/actions/workflows/ci.yml/badge.svg)](https://github.com/maragudk/goqite/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/maragudk/goqite/graph/badge.svg?token=DxGkk2lLHF)](https://codecov.io/gh/maragudk/goqite)

goqite (pronounced Go-queue-ite) is a persistent message queue Go library built on SQLite and inspired by AWS SQS (but much simpler).

Made in 🇩🇰 by [maragu](https://www.maragu.dk/), maker of [online Go courses](https://www.golang.dk/).

## Features

- Messages are persisted in a SQLite table.
- Messages are sent to and received from the queue, and are guaranteed to not be redelivered before a timeout occurs.
- Support for multiple queues in one table.
- Message timeouts can be extended, to support e.g. long-running tasks.
- A job runner abstraction is provided on top of the queue, for your background tasks.
- A simple HTTP handler is provided for your convenience.
- No non-test dependencies. Bring your own SQLite driver.

## Examples

### Queue

```go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/maragudk/goqite"
)

func main() {
	// Bring your own database connection, since you probably already have it,
	// as well as some sort of schema migration system.
	// The schema is in the schema.sql file.
	// Alternatively, use the goqite.Setup function to create the schema.
	db, err := sql.Open("sqlite3", ":memory:?_journal=WAL&_timeout=5000&_fk=true")
	if err != nil {
		log.Fatalln(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := goqite.Setup(context.Background(), db); err != nil {
		log.Fatalln(err)
	}

	// Create a new queue named "jobs".
	// You can also customize the message redelivery timeout and maximum receive count,
	// but here, we use the defaults.
	q := goqite.New(goqite.NewOpts{
		DB:   db,
		Name: "jobs",
	})

	// Send a message to the queue.
	// Note that the body is an arbitrary byte slice, so you can decide
	// what kind of payload you have. You can also set a message delay.
	err = q.Send(context.Background(), goqite.Message{
		Body: []byte("yo"),
	})
	if err != nil {
		log.Fatalln(err)
	}

	// Receive a message from the queue, during which time it's not available to
	// other consumers (until the message timeout has passed).
	m, err := q.Receive(context.Background())
	if err != nil {
		log.Fatalln(err)
	}

	fmt.Println(string(m.Body))

	// If you need more time for processing the message, you can extend
	// the message timeout as many times as you want.
	if err := q.Extend(context.Background(), m.ID, time.Second); err != nil {
		log.Fatalln(err)
	}

	// Make sure to delete the message, so it doesn't get redelivered.
	if err := q.Delete(context.Background(), m.ID); err != nil {
		log.Fatalln(err)
	}
}
```

### Jobs

```go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/maragudk/goqite"
	"github.com/maragudk/goqite/jobs"
)

func main() {
	log := slog.Default()

	// Setup the db and goqite schema.
	db, err := sql.Open("sqlite3", ":memory:?_journal=WAL&_timeout=5000&_fk=true")
	if err != nil {
		log.Info("Error opening db", "error", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := goqite.Setup(context.Background(), db); err != nil {
		log.Info("Error in setup", "error", err)
	}

	// Make a new queue for the jobs. You can have as many of these as you like, just name them differently.
	q := goqite.New(goqite.NewOpts{
		DB:   db,
		Name: "jobs",
	})

	// Make a job runner with a job limit of 1 and a short message poll interval.
	r := jobs.NewRunner(jobs.NewRunnerOpts{
		Limit:        1,
		Log:          slog.Default(),
		PollInterval: 10 * time.Millisecond,
		Queue:        q,
	})

	// Register our "print" job.
	r.Register("print", func(ctx context.Context, m []byte) error {
		fmt.Println(string(m))
		return nil
	})

	// Create a "print" job with a message.
	if err := jobs.Create(context.Background(), q, "print", []byte("Yo")); err != nil {
		log.Info("Error creating job", "error", err)
	}

	// Stop the job runner after a timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	// Start the job runner and see the job run.
	r.Start(ctx)
}
```

## Benchmarks

Just for fun, some benchmarks. 🤓

On a MacBook Pro with M3 Ultra chip and SSD, sequentially sending, receiving, and deleting a message:

```shell
$ make benchmark
go test -cpu 1,2,4,8,16 -bench=.
goos: darwin
goarch: arm64
pkg: github.com/maragudk/goqite
BenchmarkQueue/send,_receive,_delete            	   21444	     54262 ns/op
BenchmarkQueue/send,_receive,_delete-2          	   17278	     68615 ns/op
BenchmarkQueue/send,_receive,_delete-4          	   16092	     73888 ns/op
BenchmarkQueue/send,_receive,_delete-8          	   15346	     78255 ns/op
BenchmarkQueue/send,_receive,_delete-16         	   15106	     79517 ns/op
```

Note that the slowest result above is around 12,500 messages / second with 16 parallel producers/consumers.
The fastest result is around 18,500 messages / second with just one producer/consumer.
(SQLite only allows one writer at a time, so the parallelism just creates write contention.)
