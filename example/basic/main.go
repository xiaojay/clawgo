package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/anthropics/clawgo/clawgo"
)

func main() {
	cfg := clawgo.LoadConfig()

	if cfg.APIKey == "" {
		fmt.Println("Set OPENROUTER_API_KEY environment variable")
		os.Exit(1)
	}

	app := clawgo.New(cfg)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		app.Close()
		os.Exit(0)
	}()

	fmt.Printf("ClawGo starting on :%d with profile %s\n", cfg.Port, cfg.Profile)
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
