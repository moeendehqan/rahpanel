package main

import (
	"context"
	"encoding/json"
	"flag"
	"github.com/rahpanel/rahpanel/internal/agent"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	config := flag.String("config", "/etc/rahpanel/agent.json", "agent configuration")
	watch := flag.Bool("watchdog", false, "run independent fail-closed watchdog")
	flag.Parse()
	b, e := os.ReadFile(*config)
	if e != nil {
		log.Fatal("cannot read agent configuration")
	}
	var c agent.Config
	if e = json.Unmarshal(b, &c); e != nil {
		log.Fatal("invalid agent configuration")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *watch {
		e = agent.Watchdog(ctx, c.Directory)
	} else {
		e = agent.Run(ctx, c)
	}
	if e != nil {
		log.Fatal(e)
	}
}
