// Package cli defines the gtasks command tree.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/xnmp/gtasks-cli/internal/app"
	"github.com/xnmp/gtasks-cli/internal/domain"
	"github.com/xnmp/gtasks-cli/internal/render"
)

// Env holds the process-level dependencies so commands can be tested with fakes.
type Env struct {
	In       io.Reader
	Out      io.Writer
	Err      io.Writer
	Today    func() domain.Date
	NewStore func(ctx context.Context) (app.Store, error)
	// Numbers remembers the numbering shown by the last `ls`.
	Numbers app.NumberStore
	// IsTerminal reports whether In is interactive (used for confirmations).
	IsTerminal func() bool
	Auth       AuthEnv
	Version    string
}

type globals struct {
	list string
	json bool
}

func NewRoot(env Env) *cobra.Command {
	g := &globals{}
	root := &cobra.Command{
		Use:           "gtasks",
		Short:         "Manage Google Tasks from the command line",
		Version:       env.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetIn(env.In)
	root.SetOut(env.Out)
	root.SetErr(env.Err)
	root.PersistentFlags().StringVarP(&g.list, "list", "l", os.Getenv("GTASKS_LIST"),
		"task list (title, title prefix or ID; default: $GTASKS_LIST or your default list)")
	root.PersistentFlags().BoolVar(&g.json, "json", false, "output JSON")

	svc := func(cmd *cobra.Command) (app.Service, error) {
		st, err := env.NewStore(cmd.Context())
		if err != nil {
			return app.Service{}, err
		}
		return app.Service{Store: st, Numbers: env.Numbers, Today: env.Today}, nil
	}

	root.AddCommand(
		lsCmd(env, g, svc),
		addCmd(env, g, svc),
		statusCmd(env, g, svc, "done", "Mark tasks completed", domain.StatusCompleted, "Completed"),
		statusCmd(env, g, svc, "undo", "Mark completed tasks as pending again", domain.StatusNeedsAction, "Reopened"),
		rmCmd(env, g, svc),
		editCmd(env, g, svc),
		showCmd(env, g, svc),
		clearCmd(env, g, svc),
		listsCmd(env, g, svc),
		authCmd(env),
	)
	return root
}

type svcFactory func(*cobra.Command) (app.Service, error)

const refHelp = `A task is referenced by the number the last "gtasks ls" showed for it,
its ID (or a unique ID prefix of 4+ characters), or its exact title.`

func lsCmd(env Env, g *globals, svc svcFactory) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List tasks",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := svc(cmd)
			if err != nil {
				return err
			}
			// Only human-facing listings renumber; scripts use IDs.
			snap, err := s.Listing(cmd.Context(), g.list, all, !g.json)
			if errors.Is(err, app.ErrNumbersNotSaved) {
				fmt.Fprintf(env.Err, "warning: %v\n", err)
			} else if err != nil {
				return err
			}
			if g.json {
				return render.JSON(env.Out, snap)
			}
			render.Rows(env.Out, snap.List, snap.Rows, env.Today())
			return nil
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include completed tasks")
	return cmd
}

func addCmd(env Env, g *globals, svc svcFactory) *cobra.Command {
	var in app.AddInput
	cmd := &cobra.Command{
		Use:   "add TITLE...",
		Short: "Add a task",
		Long:  "Add a task. Due dates accept YYYY-MM-DD, today, tomorrow, +3d, +2w or a weekday name (its next occurrence).",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := svc(cmd)
			if err != nil {
				return err
			}
			in.ListRef = g.list
			in.Title = strings.Join(args, " ")
			t, err := s.Add(cmd.Context(), in)
			if err != nil {
				return err
			}
			return report(env, g, "Added", []domain.Task{t})
		},
	}
	cmd.Flags().StringVarP(&in.Due, "due", "d", "", "due date")
	cmd.Flags().StringVarP(&in.Notes, "notes", "n", "", "notes")
	cmd.Flags().StringVarP(&in.ParentRef, "parent", "p", "", "create as a subtask of this task")
	return cmd
}

func statusCmd(env Env, g *globals, svc svcFactory, use, short string, status domain.Status, verb string) *cobra.Command {
	return &cobra.Command{
		Use:   use + " TASK...",
		Short: short,
		Long:  short + ".\n\n" + refHelp,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := svc(cmd)
			if err != nil {
				return err
			}
			ts, err := s.SetStatus(cmd.Context(), g.list, args, status)
			if rerr := report(env, g, verb, ts); err == nil {
				err = rerr
			}
			return err
		},
	}
}

func rmCmd(env Env, g *globals, svc svcFactory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm TASK...",
		Aliases: []string{"delete"},
		Short:   "Delete tasks",
		Long: "Delete tasks. Deleting a task also deletes its subtasks; that, or deleting\n" +
			"a task assigned from Google Docs/Chat, asks for confirmation unless --yes.\n\n" + refHelp,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := svc(cmd)
			if err != nil {
				return err
			}
			approve := func(plans []app.DeletePlan) error {
				if yes {
					return nil
				}
				var warn []string
				for _, p := range plans {
					if n := len(p.Subtasks); n > 0 {
						warn = append(warn, fmt.Sprintf("%q has %d subtask(s) that will also be deleted", render.Sanitize(p.Task.Title), n))
					}
					if p.Task.Assigned {
						warn = append(warn, fmt.Sprintf("%q is assigned from Docs/Chat; the original will also be deleted", render.Sanitize(p.Task.Title)))
					}
				}
				if len(warn) == 0 {
					return nil
				}
				ok, err := confirm(env, strings.Join(warn, "\n")+"\nDelete?")
				if err != nil {
					return err
				}
				if !ok {
					return errors.New("aborted")
				}
				return nil
			}
			ts, err := s.Delete(cmd.Context(), g.list, args, approve)
			if rerr := report(env, g, "Deleted", ts); err == nil {
				err = rerr
			}
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	return cmd
}

func editCmd(env Env, g *globals, svc svcFactory) *cobra.Command {
	var title, notes, due string
	var noDue bool
	cmd := &cobra.Command{
		Use:   "edit TASK",
		Short: "Change a task's title, notes or due date",
		Long:  "Change a task's title, notes or due date. --notes \"\" clears notes.\n\n" + refHelp,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if noDue && cmd.Flags().Changed("due") {
				return fmt.Errorf("--due and --no-due are mutually exclusive")
			}
			in := app.EditInput{ListRef: g.list, Ref: args[0]}
			if cmd.Flags().Changed("title") {
				in.Title = &title
			}
			if cmd.Flags().Changed("notes") {
				in.Notes = &notes
			}
			if cmd.Flags().Changed("due") {
				if strings.TrimSpace(due) == "" {
					return fmt.Errorf("--due needs a value (use --no-due to clear)")
				}
				in.Due = &due
			}
			if noDue {
				empty := ""
				in.Due = &empty
			}
			s, err := svc(cmd)
			if err != nil {
				return err
			}
			t, err := s.Edit(cmd.Context(), in)
			if err != nil {
				return err
			}
			return report(env, g, "Updated", []domain.Task{t})
		},
	}
	cmd.Flags().StringVarP(&title, "title", "t", "", "new title")
	cmd.Flags().StringVarP(&notes, "notes", "n", "", "new notes")
	cmd.Flags().StringVarP(&due, "due", "d", "", "new due date")
	cmd.Flags().BoolVar(&noDue, "no-due", false, "remove the due date")
	return cmd
}

func showCmd(env Env, g *globals, svc svcFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "show TASK",
		Short: "Show a task's details",
		Long:  "Show a task's details.\n\n" + refHelp,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := svc(cmd)
			if err != nil {
				return err
			}
			t, err := s.Show(cmd.Context(), g.list, args[0])
			if err != nil {
				return err
			}
			if g.json {
				return render.JSON(env.Out, t)
			}
			render.Task(env.Out, t, env.Today())
			return nil
		},
	}
}

func clearCmd(env Env, g *globals, svc svcFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Hide all completed tasks in the list",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := svc(cmd)
			if err != nil {
				return err
			}
			l, err := s.ClearCompleted(cmd.Context(), g.list)
			if err != nil {
				return err
			}
			if !g.json {
				fmt.Fprintf(env.Out, "Cleared completed tasks from %s\n", render.Sanitize(l.Title))
			}
			return nil
		},
	}
}

// report prints the tasks a command changed. It is called even when the
// command failed part-way, so the user learns what did change.
func report(env Env, g *globals, verb string, ts []domain.Task) error {
	if g.json {
		if ts == nil {
			ts = []domain.Task{}
		}
		return render.JSON(env.Out, ts)
	}
	for _, t := range ts {
		fmt.Fprintf(env.Out, "%s: %s\n", verb, render.Sanitize(t.Title))
	}
	return nil
}
