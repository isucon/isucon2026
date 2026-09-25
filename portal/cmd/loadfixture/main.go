// loadfixture writes a reproducible psql fixture to stdout and statistics to stderr.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/isucon/isucon2026/portal/internal/loadfixture"
)

func run() error {
	c := loadfixture.DefaultConfig()
	start := c.Start.Format(time.RFC3339)
	scoresOnly := flag.Bool("scores-only", false, "update existing fixture scores; requires original start, seed and id-offset")
	flag.IntVar(&c.IDOffset, "id-offset", 0, "append above this unused ID boundary")
	flag.IntVar(&c.Teams, "teams", c.Teams, "number of teams")
	flag.DurationVar(&c.Duration, "duration", c.Duration, "contest duration")
	flag.StringVar(&start, "start", start, "contest start (RFC3339 or recent for a history ending now)")
	flag.Int64Var(&c.Seed, "seed", c.Seed, "random seed")
	flag.IntVar(&c.LogBytes, "log-bytes", c.LogBytes, "stdout bytes per result")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	var err error
	if start == "recent" {
		c.Start = time.Now().UTC().Add(-c.Duration)
	} else {
		c.Start, err = time.Parse(time.RFC3339, start)
	}
	if err != nil {
		return err
	}
	d, err := loadfixture.Generate(c)
	if err != nil {
		return err
	}
	if *scoresOnly {
		err = d.WriteScoreUpdateSQL(os.Stdout)
	} else {
		err = d.WriteSQL(os.Stdout)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stderr).Encode(d.Summary())
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
