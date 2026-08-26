package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/theoutdoorprogrammer/trailwire/internal/config"
	trailwirehook "github.com/theoutdoorprogrammer/trailwire/internal/hook"
	"github.com/theoutdoorprogrammer/trailwire/internal/installer"
	"github.com/theoutdoorprogrammer/trailwire/internal/mcpserver"
	"github.com/theoutdoorprogrammer/trailwire/internal/session"
	"github.com/theoutdoorprogrammer/trailwire/internal/store"
	"github.com/urfave/cli/v3"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	command := &cli.Command{
		Name:    "trailwire",
		Usage:   "Local coordination for AI coding agents",
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "config", Usage: "configuration file path"},
			&cli.StringFlag{Name: "harness", Value: "human", Usage: "sender identity to use"},
		},
		Commands: []*cli.Command{
			initCommand(),
			hookCommand(),
			mcpCommand(),
			sendCommand(),
			messageCommand(),
			announceCommand(),
			doneCommand(),
			inboxCommand(),
			agentsCommand(),
			channelCommand(),
			configCommand(),
			statusCommand(),
			{
				Name:  "version",
				Usage: "Print version details",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					fmt.Fprintf(cmd.Writer, "trailwire %s (%s)\n", version, commit)
					return nil
				},
			},
		},
	}
	if err := command.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "trailwire:", err)
		os.Exit(1)
	}
}

func initCommand() *cli.Command {
	return &cli.Command{
		Name:  "init",
		Usage: "Configure Claude Code, Codex, and Cursor",
		Flags: []cli.Flag{&cli.BoolFlag{Name: "dry-run", Usage: "show which files would change without writing"}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			result, err := installer.Install(installer.Options{
				ConfigPath: cmd.String("config"), DryRun: cmd.Bool("dry-run"),
			})
			if err != nil {
				return err
			}
			for _, file := range result.Files {
				status := "unchanged"
				if file.Changed && cmd.Bool("dry-run") {
					status = "would update"
				} else if file.Changed {
					status = "updated"
				}
				fmt.Fprintf(cmd.Writer, "%s: %s\n", status, file.Path)
			}
			return nil
		},
	}
}

func hookCommand() *cli.Command {
	return &cli.Command{
		Name:      "hook",
		Usage:     "Handle one harness hook event from stdin",
		ArgsUsage: "claude|codex|cursor",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Args().Len() != 1 {
				return errors.New("harness must be claude, codex, or cursor")
			}
			return trailwirehook.Run(ctx, trailwirehook.Options{
				Harness: cmd.Args().First(), ConfigPath: cmd.String("config"), Input: cmd.Reader, Output: cmd.Writer,
			})
		},
	}
}

func mcpCommand() *cli.Command {
	return &cli.Command{
		Name:  "mcp",
		Usage: "Run the Trailwire stdio MCP server",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			s, err := openSession(ctx, cmd, false)
			if err != nil {
				return err
			}
			defer s.Close()
			return mcpserver.New(s, version).Run(ctx)
		},
	}
}

func sendCommand() *cli.Command {
	return &cli.Command{
		Name:      "send",
		Usage:     "Send a repository, channel, or direct message",
		ArgsUsage: "MESSAGE",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "repo", Usage: "send to agents active in this repository"},
			&cli.StringFlag{Name: "channel", Aliases: []string{"c"}, Usage: "send to a named channel"},
			&cli.StringFlag{Name: "to", Aliases: []string{"t"}, Usage: "send directly to an agent id, name, or harness"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			body := strings.TrimSpace(strings.Join(cmd.Args().Slice(), " "))
			if body == "" {
				return errors.New("message is required")
			}
			targets := 0
			if cmd.Bool("repo") {
				targets++
			}
			if cmd.String("channel") != "" {
				targets++
			}
			if cmd.String("to") != "" {
				targets++
			}
			if targets != 1 {
				return errors.New("choose exactly one of --repo, --channel, or --to")
			}

			s, err := openSession(ctx, cmd, cmd.Bool("repo"))
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.Touch(ctx, "cli"); err != nil {
				return err
			}

			request := store.SendRequest{SenderID: s.Agent.ID, Body: body}
			switch {
			case cmd.Bool("repo"):
				request.TargetKind, request.TargetID = "repo", s.Repository.ID
			case cmd.String("channel") != "":
				request.TargetKind, request.TargetID = "channel", cmd.String("channel")
			default:
				target, err := s.Store.ResolveAgent(ctx, cmd.String("to"))
				if err != nil {
					return err
				}
				request.TargetKind, request.TargetID = "agent", target.ID
			}
			id, recipients, err := s.Store.Send(ctx, request)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.Writer, "sent message %d to %d agent(s)\n", id, recipients)
			return nil
		},
	}
}

func announceCommand() *cli.Command {
	return &cli.Command{
		Name:      "announce",
		Usage:     "Tell repository peers what you are changing",
		ArgsUsage: "SUMMARY",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{Name: "path", Aliases: []string{"p"}, Usage: "affected path, repeatable"},
			&cli.DurationFlag{Name: "ttl", Value: 4 * time.Hour, Usage: "how long the work intent stays active"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			summary := strings.TrimSpace(strings.Join(cmd.Args().Slice(), " "))
			if summary == "" {
				return errors.New("summary is required")
			}
			s, err := openSession(ctx, cmd, true)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.Touch(ctx, "cli"); err != nil {
				return err
			}
			intent := store.Intent{
				AgentID: s.Agent.ID, RepoID: s.Repository.ID, Summary: summary,
				Paths: cmd.StringSlice("path"), ExpiresAt: time.Now().Add(cmd.Duration("ttl")),
			}
			if err := s.Store.SetIntent(ctx, intent); err != nil {
				return err
			}
			body := "Working on: " + summary
			if len(intent.Paths) > 0 {
				body += "\nAffected paths: " + strings.Join(intent.Paths, ", ")
			}
			_, recipients, err := s.Store.Send(ctx, store.SendRequest{
				SenderID: s.Agent.ID, TargetKind: "repo", TargetID: s.Repository.ID, Body: body,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.Writer, "announced work to %d repository peer(s)\n", recipients)
			return nil
		},
	}
}

func doneCommand() *cli.Command {
	return &cli.Command{
		Name:  "done",
		Usage: "Clear your active work intent in this repository",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			s, err := openSession(ctx, cmd, true)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.Store.ClearIntent(ctx, s.Agent.ID, s.Repository.ID); err != nil {
				return err
			}
			fmt.Fprintln(cmd.Writer, "cleared active work intent")
			return nil
		},
	}
}

func inboxCommand() *cli.Command {
	return &cli.Command{
		Name:  "inbox",
		Usage: "Claim and print unread messages",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			s, err := openSession(ctx, cmd, false)
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.Touch(ctx, "cli"); err != nil {
				return err
			}
			messages, err := s.Store.ClaimInbox(ctx, s.Agent.ID, 50)
			if err != nil {
				return err
			}
			for _, message := range messages {
				label := message.TargetKind
				if message.EventKind != "created" {
					label = fmt.Sprintf("%s message %d %s", label, message.ID, message.EventKind)
				}
				fmt.Fprintf(cmd.Writer, "[%s] %s: %s\n", label, message.SenderName, message.Body)
			}
			if len(messages) == 0 {
				fmt.Fprintln(cmd.Writer, "inbox is empty")
			}
			return nil
		},
	}
}

func agentsCommand() *cli.Command {
	return &cli.Command{
		Name:  "agents",
		Usage: "List known agents",
		Flags: []cli.Flag{&cli.BoolFlag{Name: "repo", Usage: "only agents seen in this repository"}},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			s, err := openSession(ctx, cmd, cmd.Bool("repo"))
			if err != nil {
				return err
			}
			defer s.Close()
			repoID := ""
			if cmd.Bool("repo") {
				repoID = s.Repository.ID
			}
			agents, err := s.Store.Agents(ctx, repoID)
			if err != nil {
				return err
			}
			for _, agent := range agents {
				fmt.Fprintf(cmd.Writer, "%s\t%s\t%s\n", agent.ID, agent.Harness, agent.Name)
			}
			return nil
		},
	}
}

func channelCommand() *cli.Command {
	return &cli.Command{
		Name:  "channel",
		Usage: "Manage standalone channels",
		Commands: []*cli.Command{
			{
				Name: "create", ArgsUsage: "CHANNEL", Usage: "Create a channel and join it",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() != 1 {
						return errors.New("channel name is required")
					}
					s, err := openSession(ctx, cmd, false)
					if err != nil {
						return err
					}
					defer s.Close()
					name := cmd.Args().First()
					if err := s.Store.CreateChannel(ctx, name); err != nil {
						return err
					}
					if err := s.Store.JoinChannel(ctx, s.Agent.ID, name); err != nil {
						return err
					}
					fmt.Fprintf(cmd.Writer, "created and joined #%s\n", strings.TrimPrefix(name, "#"))
					return nil
				},
			},
			{
				Name: "join", ArgsUsage: "CHANNEL",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() != 1 {
						return errors.New("channel name is required")
					}
					s, err := openSession(ctx, cmd, false)
					if err != nil {
						return err
					}
					defer s.Close()
					if err := s.Store.JoinChannel(ctx, s.Agent.ID, cmd.Args().First()); err != nil {
						return err
					}
					fmt.Fprintf(cmd.Writer, "joined #%s\n", strings.TrimPrefix(cmd.Args().First(), "#"))
					return nil
				},
			},
			{
				Name: "leave", ArgsUsage: "CHANNEL",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() != 1 {
						return errors.New("channel name is required")
					}
					s, err := openSession(ctx, cmd, false)
					if err != nil {
						return err
					}
					defer s.Close()
					return s.Store.LeaveChannel(ctx, s.Agent.ID, cmd.Args().First())
				},
			},
			{
				Name: "list",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					s, err := openSession(ctx, cmd, false)
					if err != nil {
						return err
					}
					defer s.Close()
					channels, err := s.Store.Channels(ctx, s.Agent.ID)
					if err != nil {
						return err
					}
					for _, channel := range channels {
						fmt.Fprintln(cmd.Writer, "#"+channel)
					}
					return nil
				},
			},
		},
	}
}

func messageCommand() *cli.Command {
	return &cli.Command{
		Name:  "message",
		Usage: "Modify or recant a message you sent",
		Commands: []*cli.Command{
			{
				Name: "modify", ArgsUsage: "MESSAGE_ID NEW_MESSAGE",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() < 2 {
						return errors.New("message id and replacement message are required")
					}
					messageID, err := strconv.ParseInt(cmd.Args().First(), 10, 64)
					if err != nil {
						return fmt.Errorf("invalid message id: %w", err)
					}
					s, err := openSession(ctx, cmd, false)
					if err != nil {
						return err
					}
					defer s.Close()
					recipients, err := s.Store.ModifyMessage(ctx, s.Agent.ID, messageID, strings.Join(cmd.Args().Slice()[1:], " "))
					if err != nil {
						return err
					}
					fmt.Fprintf(cmd.Writer, "modified message %d for %d recipient(s)\n", messageID, recipients)
					return nil
				},
			},
			{
				Name: "recant", ArgsUsage: "MESSAGE_ID [REASON]",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() < 1 {
						return errors.New("message id is required")
					}
					messageID, err := strconv.ParseInt(cmd.Args().First(), 10, 64)
					if err != nil {
						return fmt.Errorf("invalid message id: %w", err)
					}
					reason := ""
					if cmd.Args().Len() > 1 {
						reason = strings.Join(cmd.Args().Slice()[1:], " ")
					}
					s, err := openSession(ctx, cmd, false)
					if err != nil {
						return err
					}
					defer s.Close()
					recipients, err := s.Store.RecantMessage(ctx, s.Agent.ID, messageID, reason)
					if err != nil {
						return err
					}
					fmt.Fprintf(cmd.Writer, "recanted message %d for %d recipient(s)\n", messageID, recipients)
					return nil
				},
			},
		},
	}
}

func configCommand() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "Manage human-owned Trailwire settings",
		Commands: []*cli.Command{
			{
				Name: "retention", ArgsUsage: "DURATION", Usage: "Set global message retention",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() != 1 {
						return errors.New("retention duration is required")
					}
					ttl, err := time.ParseDuration(cmd.Args().First())
					if err != nil {
						return fmt.Errorf("invalid retention duration: %w", err)
					}
					s, err := openSession(ctx, cmd, false)
					if err != nil {
						return err
					}
					defer s.Close()
					if err := s.Config.SetMessageTTL(ttl); err != nil {
						return err
					}
					if err := config.Save(s.ConfigPath, s.Config); err != nil {
						return err
					}
					fmt.Fprintf(cmd.Writer, "message retention set to %s\n", ttl)
					return nil
				},
			},
		},
	}
}

func statusCommand() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "Show local Trailwire identity and storage",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			s, err := openSession(ctx, cmd, false)
			if err != nil {
				return err
			}
			defer s.Close()
			fmt.Fprintf(cmd.Writer, "agent: %s (%s)\n", s.Agent.Name, s.Agent.ID)
			fmt.Fprintf(cmd.Writer, "database: %s\n", s.Config.Database)
			if s.Repository != nil {
				fmt.Fprintf(cmd.Writer, "repository: %s (%s)\n", s.Repository.Display, s.Repository.ID)
			}
			return nil
		},
	}
}

func openSession(ctx context.Context, cmd *cli.Command, requireRepo bool) (*session.Session, error) {
	return session.Open(ctx, session.Options{
		ConfigPath: cmd.String("config"), Harness: cmd.String("harness"), RequireRepo: requireRepo,
	})
}
