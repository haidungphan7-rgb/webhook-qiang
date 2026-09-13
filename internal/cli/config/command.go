// Package config implements `webhook-zq config`: one place to read, write and - most
// importantly - explain the configuration.
//
// The problem it solves is not "the settings are hard to change". It is that the same
// setting used to live in four places (CLI flag, environment variable, the desktop
// tool's JSON file, and a generated .cmd launcher) and two of them were written by
// different code, so the answer to "what is this instance actually running with?" was
// spread across a process list, a registry and a file nobody opened.
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"
	"go.uber.org/zap"

	"github.com/yuandzhang/webhook-zq/internal/config"
)

// NewCommand builds the `config` command tree.
func NewCommand(log *zap.Logger) *cli.Command {
	configFlag := cli.StringFlag{
		Name:    "config",
		Usage:   "Configuration file to act on (defaults to %APPDATA%/webhook-zq/config.json)",
		Sources: cli.EnvVars("WEBHOOK_ZQ_CONFIG"),
	}

	resolve := func(c *cli.Command) (string, error) {
		if p := c.String("config"); p != "" {
			return p, nil
		}

		return config.DefaultPath()
	}

	return &cli.Command{
		Name:  "config",
		Usage: "Inspect and edit the configuration file",
		Commands: []*cli.Command{
			{
				Name:  "path",
				Usage: "Print the path of the configuration file",
				Action: func(_ context.Context, c *cli.Command) error {
					p, err := resolve(c)
					if err != nil {
						return err
					}

					fmt.Fprintln(c.Root().Writer, p)

					return nil
				},
			},
			{
				Name:  "show",
				Usage: "Print the file contents (secrets are never stored there)",
				Flags: []cli.Flag{&configFlag},
				Action: func(_ context.Context, c *cli.Command) error {
					p, err := resolve(c)
					if err != nil {
						return err
					}

					f, migrated, err := config.Load(p)
					if err != nil {
						return err
					}

					data, err := marshal(f)
					if err != nil {
						return err
					}

					fmt.Fprintf(c.Root().Writer, "# %s\n%s", p, data)

					if migrated {
						fmt.Fprintf(c.Root().ErrWriter,
							"note: this file is in the pre-1.0 PascalCase shape; run `webhook-zq config load --write` to convert it\n")
					}

					return nil
				},
			},
			{
				Name:      "set",
				Usage:     "Set one or more values, e.g. config set port=8080 replay_allow_private=true",
				ArgsUsage: "KEY=VALUE [KEY=VALUE ...]",
				Flags:     []cli.Flag{&configFlag},
				Action: func(_ context.Context, c *cli.Command) error {
					p, err := resolve(c)
					if err != nil {
						return err
					}

					f, _, err := config.Load(p)
					if err != nil {
						return err
					}

					for _, arg := range c.Args().Slice() {
						key, value, ok := strings.Cut(arg, "=")
						if !ok {
							return fmt.Errorf("expected KEY=VALUE, got %q", arg)
						}

						for _, s := range config.SecretFields() {
							if s.Key == key {
								return fmt.Errorf(
									"%s is a deployment secret and is never written to the config file; set %s in the environment instead",
									key, s.Env)
							}
						}

						field, ok := config.Lookup(key)
						if !ok {
							return fmt.Errorf("unknown key %q (run `webhook-zq config explain` to list them)", key)
						}

						if err := field.Set(f, value); err != nil {
							return err
						}
					}

					if err := config.Save(p, f); err != nil {
						return err
					}

					fmt.Fprintf(c.Root().Writer, "saved %s\n", p)

					return nil
				},
			},
			{
				Name:  "explain",
				Usage: "Show every setting, its effective value and where that value came from",
				Flags: []cli.Flag{&configFlag},
				Action: func(_ context.Context, c *cli.Command) error {
					p, err := resolve(c)
					if err != nil {
						return err
					}

					f, _, err := config.Load(p)
					if err != nil {
						return err
					}

					fmt.Fprintf(c.Root().Writer, "config file: %s\n\n", p)
					fmt.Fprintf(c.Root().Writer, "%-24s %-30s %s\n", "KEY", "EFFECTIVE VALUE", "SOURCE")
					fmt.Fprintln(c.Root().Writer, strings.Repeat("-", 72))

					for _, field := range config.Fields() {
						value, source := effective(field, f)
						fmt.Fprintf(c.Root().Writer, "%-24s %-30s %s\n", field.Key, truncate(value, 30), source)
					}

					fmt.Fprintln(c.Root().Writer)

					// Secrets get their own section: the question that matters is answered
					// without the value ever being printed.
					for _, s := range config.SecretFields() {
						state := "not set"
						source := "-"

						if os.Getenv(s.Env) != "" {
							state = "set"
							source = "env"
						}

						fmt.Fprintf(c.Root().Writer, "%-24s %-30s %s\n", s.Key, state+" (value hidden)", source)
					}

					fmt.Fprintln(c.Root().Writer)
					fmt.Fprintln(c.Root().Writer,
						"precedence: command line flag > environment variable > config file > default")

					return nil
				},
			},
			{
				Name:  "expand",
				Usage: "Render the configuration as `start` arguments or as environment lines",
				Flags: []cli.Flag{
					&configFlag,
					&cli.StringFlag{
						Name:  "format",
						Value: "argv",
						Usage: "argv (one line of flags) or env (KEY=VALUE lines for a launcher script)",
						Validator: func(s string) error {
							if s != "argv" && s != "env" {
								return fmt.Errorf("format must be argv or env, got %q", s)
							}

							return nil
						},
					},
					&cli.BoolFlag{
						Name:  "include-secrets",
						Usage: "Print secret values too (needed when generating a launcher script)",
					},
				},
				Action: func(_ context.Context, c *cli.Command) error {
					p, err := resolve(c)
					if err != nil {
						return err
					}

					f, _, err := config.Load(p)
					if err != nil {
						return err
					}

					out := c.Root().Writer

					if c.String("format") == "env" {
						for _, field := range config.Fields() {
							value, _ := effective(field, f)
							if value == "" {
								continue
							}

							// The raw value, not the masked one: this output becomes a
							// launcher script, and a masked password would simply not work.
							fmt.Fprintf(out, "%s=%s\n", field.Env, rawValue(field, f))
						}

						for _, s := range config.SecretFields() {
							v := os.Getenv(s.Env)
							if v == "" {
								continue
							}

							if !c.Bool("include-secrets") {
								continue
							}

							fmt.Fprintf(out, "%s=%s\n", s.Env, v)
						}

						return nil
					}

					parts := make([]string, 0, len(config.Fields()))

					for _, field := range config.Fields() {
						value, _ := effective(field, f)
						if value == "" || value == "false" || value == "0" {
							continue
						}

						// A connection string in argv shows up in the process list, readable
						// by every user on the machine. It belongs in the environment.
						if field.Key == "database_url" {
							continue
						}

						if field.Key == "replay_allow_hosts" {
							for _, h := range splitHosts(value) {
								parts = append(parts, field.Flag, h)
							}

							continue
						}

						if value == "true" {
							parts = append(parts, field.Flag)

							continue
						}

						parts = append(parts, field.Flag, value)
					}

					fmt.Fprintln(out, strings.Join(parts, " "))

					return nil
				},
			},
			{
				Name:  "load",
				Usage: "Read the file, translating the pre-1.0 PascalCase shape if needed",
				Flags: []cli.Flag{
					&configFlag,
					&cli.BoolFlag{
						Name:  "write",
						Usage: "Write the translated file back (a .bak is kept)",
					},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					p, err := resolve(c)
					if err != nil {
						return err
					}

					f, migrated, err := config.Load(p)
					if err != nil {
						return err
					}

					if !migrated {
						fmt.Fprintf(c.Root().Writer, "%s is already in the current format\n", p)

						return nil
					}

					if !c.Bool("write") {
						fmt.Fprintf(c.Root().Writer,
							"%s is in the pre-1.0 format; re-run with --write to convert it (a .bak is kept)\n", p)

						return nil
					}

					if err := config.Save(p, f); err != nil {
						return err
					}

					fmt.Fprintf(c.Root().Writer, "converted %s (previous version kept as %s.bak)\n", p, p)

					return nil
				},
			},
		},
	}
}

// effective returns the value a field would actually run with, and where it came from.
func effective(field config.Field, f *config.File) (string, string) {
	if v := strings.TrimSpace(os.Getenv(field.Env)); v != "" {
		if field.Mask != nil {
			return field.Mask(v), "env"
		}

		return v, "env"
	}

	if v := field.Get(f); strings.TrimSpace(v) != "" && v != "0" && v != "false" {
		if field.Mask != nil {
			return field.Mask(v), "file"
		}

		return v, "file"
	}

	return "", "default"
}

// rawValue is the unmasked value, used only when building a launcher script.
func rawValue(field config.Field, f *config.File) string {
	if v := strings.TrimSpace(os.Getenv(field.Env)); v != "" {
		return v
	}

	return field.Get(f)
}

func splitHosts(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n-1] + "…"
}

func marshal(f *config.File) (string, error) {
	// The file has no secrets, so a straight marshal is safe to print.
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", err
	}

	return string(data) + "\n", nil
}
