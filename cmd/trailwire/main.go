package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	trailwiredocs "github.com/theoutdoorprogrammer/trailwire"
	"github.com/theoutdoorprogrammer/trailwire/internal/config"
	trailwirehook "github.com/theoutdoorprogrammer/trailwire/internal/hook"
	"github.com/theoutdoorprogrammer/trailwire/internal/installer"
	"github.com/theoutdoorprogrammer/trailwire/internal/mcpserver"
	"github.com/theoutdoorprogrammer/trailwire/internal/session"
	"github.com/theoutdoorprogrammer/trailwire/internal/store"
	"github.com/theoutdoorprogrammer/trailwire/internal/telemetry"
	"github.com/theoutdoorprogrammer/trailwire/internal/watch"
	"github.com/urfave/cli/v3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	buildInfo, _ := debug.ReadBuildInfo()
	version, commit = resolveBuildVersion(version, commit, buildInfo)
	ctx := context.Background()
	shutdown, err := telemetry.Setup(ctx, version)
	if err != nil {
		fmt.Fprintln(os.Stderr, "trailwire:", err)
		os.Exit(1)
	}
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(shutdownContext); err != nil {
			fmt.Fprintln(os.Stderr, "trailwire: shut down telemetry:", err)
		}
	}()
	ctx, span := otel.Tracer("github.com/theoutdoorprogrammer/trailwire").Start(ctx, "trailwire.run")
	defer span.End()

	command := &cli.Command{
		Name:    "trailwire",
		Usage:   "Local coordination for AI coding agents",
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "config", Usage: "configuration file path"},
			&cli.StringFlag{Name: "harness", Value: "human", Usage: "sender identity to use"},
		},
		Commands: []*cli.Command{
			{
				Name:  "skill",
				Usage: "Print the embedded agent-facing documentation",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					fmt.Fprint(cmd.Writer, trailwiredocs.README)
					return nil
				},
			},
			initCommand(),
			hookCommand(),
			mcpCommand(),
			sendCommand(),
			messageCommand(),
			announceCommand(),
			doneCommand(),
			inboxCommand(),
			watchCommand(),
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
	if err := command.Run(ctx, os.Args); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "command failed")
		fmt.Fprintln(os.Stderr, "trailwire:", err)
		os.Exit(1)
	}
}

func resolveBuildVersion(linkedVersion, linkedCommit string, info *debug.BuildInfo) (string, string) {
	resolvedVersion := linkedVersion
	resolvedCommit := linkedCommit
	if info == nil {
		return resolvedVersion, resolvedCommit
	}
	if resolvedVersion == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		resolvedVersion = strings.TrimPrefix(info.Main.Version, "v")
	}
	if resolvedCommit == "unknown" {
		for _, setting := range info.Settings {
			if setting.Key != "vcs.revision" || setting.Value == "" {
				continue
			}
			resolvedCommit = setting.Value
			if len(resolvedCommit) > 7 {
				resolvedCommit = resolvedCommit[:7]
			}
			break
		}
	}
	return resolvedVersion, resolvedCommit
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
			s, err := session.Open(ctx, session.Options{
				ConfigPath: cmd.String("config"), Harness: cmd.String("harness"),
			})
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

func watchCommand() *cli.Command {
	return &cli.Command{
		Name:  "watch",
		Usage: "Watch all unexpired agent conversations in a live TUI",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if !strings.EqualFold(cmd.String("harness"), "human") {
				return errors.New("watch is human-only; use the default human harness")
			}
			s, err := openSession(ctx, cmd, false)
			if err != nil {
				return err
			}
			defer s.Close()
			ttl, err := s.Config.MessageTTLDuration()
			if err != nil {
				return err
			}
			return watch.Run(ctx, s.Store, watch.Options{
				MessageTTL: ttl,
				Input:      cmd.Reader,
				Output:     cmd.Writer,
			})
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
						label := "#" + channel.Name
						if channel.Forced {
							label += " (required)"
						}
						fmt.Fprintln(cmd.Writer, label)
					}
					return nil
				},
			},
		},
	}
}

func forcedChannelsCommand() *cli.Command {
	return &cli.Command{
		Name:  "forced-channels",
		Usage: "Manage channels every agent must receive",
		Commands: []*cli.Command{
			{
				Name: "list", Usage: "List mandatory channels",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					s, err := openHumanConfigSession(ctx, cmd)
					if err != nil {
						return err
					}
					defer s.Close()
					for _, name := range s.Config.ForcedChannels {
						fmt.Fprintln(cmd.Writer, "#"+name)
					}
					return nil
				},
			},
			{
				Name: "set", ArgsUsage: "CHANNEL...", Usage: "Replace the mandatory channel list",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() == 0 {
						return errors.New("at least one channel is required; use clear to remove all mandatory channels")
					}
					return saveForcedChannels(ctx, cmd, cmd.Args().Slice())
				},
			},
			{
				Name: "add", ArgsUsage: "CHANNEL...", Usage: "Add mandatory channels",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() == 0 {
						return errors.New("at least one channel is required")
					}
					s, err := openHumanConfigSession(ctx, cmd)
					if err != nil {
						return err
					}
					defer s.Close()
					names := append(append([]string{}, s.Config.ForcedChannels...), cmd.Args().Slice()...)
					return persistForcedChannels(ctx, cmd, s, names)
				},
			},
			{
				Name: "remove", ArgsUsage: "CHANNEL...", Usage: "Remove mandatory channel policy without removing voluntary memberships",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Args().Len() == 0 {
						return errors.New("at least one channel is required")
					}
					s, err := openHumanConfigSession(ctx, cmd)
					if err != nil {
						return err
					}
					defer s.Close()
					removed := make(map[string]struct{}, cmd.Args().Len())
					for _, name := range cmd.Args().Slice() {
						removed[strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "#")] = struct{}{}
					}
					names := make([]string, 0, len(s.Config.ForcedChannels))
					for _, name := range s.Config.ForcedChannels {
						if _, drop := removed[name]; !drop {
							names = append(names, name)
						}
					}
					return persistForcedChannels(ctx, cmd, s, names)
				},
			},
			{
				Name: "clear", Usage: "Remove all mandatory channel policy",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return saveForcedChannels(ctx, cmd, nil)
				},
			},
		},
	}
}

func saveForcedChannels(ctx context.Context, cmd *cli.Command, names []string) error {
	s, err := openHumanConfigSession(ctx, cmd)
	if err != nil {
		return err
	}
	defer s.Close()
	return persistForcedChannels(ctx, cmd, s, names)
}

func persistForcedChannels(ctx context.Context, cmd *cli.Command, s *session.Session, names []string) error {
	if err := s.Config.SetForcedChannels(names); err != nil {
		return err
	}
	if err := config.Save(s.ConfigPath, s.Config); err != nil {
		return err
	}
	if err := s.Store.SyncForcedChannels(ctx, s.Config.ForcedChannels); err != nil {
		return err
	}
	if len(s.Config.ForcedChannels) == 0 {
		fmt.Fprintln(cmd.Writer, "mandatory channels cleared")
		return nil
	}
	fmt.Fprintf(cmd.Writer, "mandatory channels: #%s\n", strings.Join(s.Config.ForcedChannels, ", #"))
	return nil
}

func openHumanConfigSession(ctx context.Context, cmd *cli.Command) (*session.Session, error) {
	if !strings.EqualFold(cmd.String("harness"), "human") {
		return nil, errors.New("mandatory channel policy is human-owned; use the default human harness")
	}
	return openSession(ctx, cmd, false)
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
			forcedChannelsCommand(),
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
			if len(s.Config.ForcedChannels) > 0 {
				fmt.Fprintf(cmd.Writer, "mandatory channels: #%s\n", strings.Join(s.Config.ForcedChannels, ", #"))
			}
			if s.Repository != nil {
				fmt.Fprintf(cmd.Writer, "repository: %s (%s)\n", s.Repository.Display, s.Repository.ID)
			}
			return nil
		},
	}
}

func openSession(ctx context.Context, cmd *cli.Command, requireRepo bool) (*session.Session, error) {
	return session.Open(ctx, session.Options{
		ConfigPath: cmd.String("config"), Harness: cmd.String("harness"), NativeSessionID: "cli", RequireRepo: requireRepo,
	})
}
