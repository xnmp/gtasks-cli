package cli

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/xnmp/gtasks-cli/internal/domain"
	"github.com/xnmp/gtasks-cli/internal/render"
)

func listsCmd(env Env, g *globals, svc svcFactory) *cobra.Command {
	show := func(cmd *cobra.Command, _ []string) error {
		s, err := svc(cmd)
		if err != nil {
			return err
		}
		lists, err := s.Store.Lists(cmd.Context())
		if err != nil {
			return err
		}
		if g.json {
			if lists == nil {
				lists = []domain.TaskList{}
			}
			return render.JSON(env.Out, lists)
		}
		render.Lists(env.Out, lists)
		return nil
	}
	cmd := &cobra.Command{
		Use:   "lists",
		Short: "Show and manage task lists",
		Args:  cobra.NoArgs,
		RunE:  show,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:     "ls",
			Aliases: []string{"list"},
			Short:   "Show task lists",
			Args:    cobra.NoArgs,
			RunE:    show,
		},
		&cobra.Command{
			Use:   "add TITLE...",
			Short: "Create a task list",
			Args:  cobra.MinimumNArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				s, err := svc(cmd)
				if err != nil {
					return err
				}
				l, err := s.CreateList(cmd.Context(), strings.Join(args, " "))
				if err != nil {
					return err
				}
				return reportList(env, g, "Created list", l)
			},
		},
		&cobra.Command{
			Use:   "rename LIST TITLE...",
			Short: "Rename a task list",
			Args:  cobra.MinimumNArgs(2),
			RunE: func(cmd *cobra.Command, args []string) error {
				s, err := svc(cmd)
				if err != nil {
					return err
				}
				l, err := s.RenameList(cmd.Context(), args[0], strings.Join(args[1:], " "))
				if err != nil {
					return err
				}
				return reportList(env, g, "Renamed list to", l)
			},
		},
		listsRmCmd(env, g, svc),
	)
	return cmd
}

func listsRmCmd(env Env, g *globals, svc svcFactory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rm LIST",
		Short: "Delete a task list and all its tasks (LIST must be an exact title or ID)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := svc(cmd)
			if err != nil {
				return err
			}
			plan, err := s.PlanListDeletion(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !yes {
				prompt := fmt.Sprintf("Delete list %q and its %d task(s)?", render.Sanitize(plan.List.Title), plan.Tasks)
				if plan.Assigned > 0 {
					prompt = fmt.Sprintf("%d of them are assigned from Docs/Chat; their originals will also be deleted.\n%s", plan.Assigned, prompt)
				}
				ok, err := confirm(env, prompt)
				if err != nil {
					return err
				}
				if !ok {
					return fmt.Errorf("aborted")
				}
			}
			if err := s.DeleteList(cmd.Context(), plan); err != nil {
				return err
			}
			return reportList(env, g, "Deleted list", plan.List)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation")
	return cmd
}

func confirm(env Env, prompt string) (bool, error) {
	if !env.IsTerminal() {
		return false, fmt.Errorf("confirmation needed but stdin is not interactive; pass --yes")
	}
	fmt.Fprintf(env.Err, "%s [y/N] ", prompt)
	line, err := bufio.NewReader(env.In).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	ans := strings.ToLower(strings.TrimSpace(line))
	return ans == "y" || ans == "yes", nil
}

func reportList(env Env, g *globals, verb string, l domain.TaskList) error {
	if g.json {
		return render.JSON(env.Out, l)
	}
	fmt.Fprintf(env.Out, "%s: %s\n", verb, render.Sanitize(l.Title))
	return nil
}
