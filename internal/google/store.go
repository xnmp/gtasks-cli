// Package google adapts the Google Tasks API to the app.Store port.
package google

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/api/option"
	tasks "google.golang.org/api/tasks/v1"

	"github.com/xnmp/gtasks-cli/internal/domain"
)

type Store struct {
	svc *tasks.Service
}

// NewStore builds a Store from an HTTP client that already carries OAuth credentials.
func NewStore(ctx context.Context, client *http.Client) (*Store, error) {
	svc, err := tasks.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("create tasks service: %w", err)
	}
	return &Store{svc: svc}, nil
}

func (s *Store) Lists(ctx context.Context) ([]domain.TaskList, error) {
	var out []domain.TaskList
	err := s.svc.Tasklists.List().MaxResults(100).Pages(ctx, func(page *tasks.TaskLists) error {
		for _, l := range page.Items {
			out = append(out, toList(l))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list task lists: %w", err)
	}
	return out, nil
}

func (s *Store) GetList(ctx context.Context, id string) (domain.TaskList, error) {
	l, err := s.svc.Tasklists.Get(id).Context(ctx).Do()
	if err != nil {
		return domain.TaskList{}, fmt.Errorf("get list: %w", err)
	}
	return toList(l), nil
}

func (s *Store) CreateList(ctx context.Context, title string) (domain.TaskList, error) {
	l, err := s.svc.Tasklists.Insert(&tasks.TaskList{Title: title}).Context(ctx).Do()
	if err != nil {
		return domain.TaskList{}, fmt.Errorf("create list: %w", err)
	}
	return toList(l), nil
}

func (s *Store) RenameList(ctx context.Context, id, title string) (domain.TaskList, error) {
	l, err := s.svc.Tasklists.Patch(id, &tasks.TaskList{Title: title}).Context(ctx).Do()
	if err != nil {
		return domain.TaskList{}, fmt.Errorf("rename list: %w", err)
	}
	return toList(l), nil
}

func (s *Store) DeleteList(ctx context.Context, id string) error {
	if err := s.svc.Tasklists.Delete(id).Context(ctx).Do(); err != nil {
		return fmt.Errorf("delete list: %w", err)
	}
	return nil
}

func (s *Store) Tasks(ctx context.Context, listID string) ([]domain.Task, error) {
	var out []domain.Task
	call := s.svc.Tasks.List(listID).MaxResults(100).ShowCompleted(true).ShowHidden(true).ShowAssigned(true)
	err := call.Pages(ctx, func(page *tasks.Tasks) error {
		for _, t := range page.Items {
			dt, err := toTask(t)
			if err != nil {
				return err
			}
			out = append(out, dt)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	return out, nil
}

func (s *Store) CreateTask(ctx context.Context, listID, parentID string, nt domain.NewTask) (domain.Task, error) {
	body := &tasks.Task{Title: nt.Title, Notes: nt.Notes}
	if nt.Due != nil {
		body.Due = nt.Due.RFC3339()
	}
	call := s.svc.Tasks.Insert(listID, body).Context(ctx)
	if parentID != "" {
		call = call.Parent(parentID)
	}
	t, err := call.Do()
	if err != nil {
		return domain.Task{}, fmt.Errorf("create task: %w", err)
	}
	return toTask(t)
}

func (s *Store) UpdateTask(ctx context.Context, listID, taskID string, p domain.TaskPatch) (domain.Task, error) {
	t, err := s.svc.Tasks.Patch(listID, taskID, patchBody(p)).Context(ctx).Do()
	if err != nil {
		return domain.Task{}, fmt.Errorf("update task: %w", err)
	}
	return toTask(t)
}

// patchBody translates a domain patch into an API PATCH body. Clearing a
// field requires listing it in NullFields so it is sent as JSON null.
func patchBody(p domain.TaskPatch) *tasks.Task {
	body := &tasks.Task{}
	if p.Title != nil {
		body.Title = *p.Title
	}
	if p.Notes != nil {
		if *p.Notes == "" {
			body.NullFields = append(body.NullFields, "Notes")
		} else {
			body.Notes = *p.Notes
		}
	}
	if p.Due != nil {
		if p.Due.Clear {
			body.NullFields = append(body.NullFields, "Due")
		} else {
			body.Due = p.Due.Date.RFC3339()
		}
	}
	if p.Status != nil {
		body.Status = string(*p.Status)
		if *p.Status == domain.StatusNeedsAction {
			// Reopening requires removing the completion timestamp.
			body.NullFields = append(body.NullFields, "Completed")
		}
	}
	return body
}

func (s *Store) DeleteTask(ctx context.Context, listID, taskID string) error {
	if err := s.svc.Tasks.Delete(listID, taskID).Context(ctx).Do(); err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	return nil
}

func (s *Store) ClearCompleted(ctx context.Context, listID string) error {
	if err := s.svc.Tasks.Clear(listID).Context(ctx).Do(); err != nil {
		return fmt.Errorf("clear completed: %w", err)
	}
	return nil
}

func toList(l *tasks.TaskList) domain.TaskList {
	return domain.TaskList{ID: l.Id, Title: l.Title, Updated: parseTime(l.Updated)}
}

func toTask(t *tasks.Task) (domain.Task, error) {
	out := domain.Task{
		ID:          t.Id,
		Title:       t.Title,
		Notes:       t.Notes,
		Status:      domain.Status(t.Status),
		Parent:      t.Parent,
		Position:    t.Position,
		Updated:     parseTime(t.Updated),
		WebViewLink: t.WebViewLink,
		Assigned:    t.AssignmentInfo != nil,
	}
	if t.Due != "" {
		d, err := domain.ParseAPIDate(t.Due)
		if err != nil {
			return domain.Task{}, err
		}
		out.Due = &d
	}
	if t.Completed != nil && *t.Completed != "" {
		c := parseTime(*t.Completed)
		out.Completed = &c
	}
	return out, nil
}

// parseTime parses an API timestamp, returning the zero time when absent or
// malformed; these timestamps are informational only.
func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
