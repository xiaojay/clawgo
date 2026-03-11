package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/anthropics/clawgo/clawgo"
	"github.com/urfave/cli/v2"
)

func main() {
	app := &cli.App{
		Name:    "clawgo",
		Usage:   "Smart LLM routing proxy with OpenRouter backend",
		Version: clawgo.Version,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "api-key",
				EnvVars: []string{"OPENROUTER_API_KEY"},
				Usage:   "OpenRouter API key",
			},
			&cli.Int64Flag{
				Name:    "port",
				Value:   8402,
				EnvVars: []string{"CLAWGO_PORT"},
				Usage:   "Proxy listen port",
			},
			&cli.StringFlag{
				Name:    "profile",
				Value:   "auto",
				EnvVars: []string{"CLAWGO_PROFILE"},
				Usage:   "Routing profile (auto/eco/premium)",
			},
			&cli.BoolFlag{
				Name:    "debug-http",
				EnvVars: []string{"CLAWGO_DEBUG_HTTP"},
				Usage:   "Log inbound requests and OpenRouter HTTP traffic",
			},
		},
		Action: func(c *cli.Context) error {
			cfg := clawgo.LoadConfig()
			if c.IsSet("api-key") {
				cfg.APIKey = c.String("api-key")
			}
			if c.IsSet("port") {
				cfg.Port = c.Int64("port")
			}
			if c.IsSet("profile") {
				cfg.Profile = c.String("profile")
			}
			if c.IsSet("debug-http") {
				cfg.DebugHTTP = c.Bool("debug-http")
			}

			app := clawgo.New(cfg)

			// Graceful shutdown
			quit := make(chan os.Signal, 1)
			signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-quit
				log.Println("shutting down...")
				app.Close()
				os.Exit(0)
			}()

			return app.Run()
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
