package messytodo

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type Priority int

const (
	Low Priority = iota
	Medium
	High
)

type Todo struct {
	ID        int
	Title     string
	Done      bool
	Priority  Priority
	CreatedAt time.Time
	DueDate   time.Time
	Tags      []string
}

type TodoList struct {
	todos  []*Todo
	nextID int
}

func NewTodoList() *TodoList {
	return &TodoList{nextID: 1}
}

func (tl *TodoList) Add(title string, priority Priority, tags []string) *Todo {
	t := &Todo{
		ID:        tl.nextID,
		Title:     title,
		Done:      false,
		Priority:  priority,
		CreatedAt: time.Now(),
		Tags:      tags,
	}
	tl.nextID++
	tl.todos = append(tl.todos, t)
	return t
}

func (tl *TodoList) Complete(id int) error {
	for _, t := range tl.todos {
		if t.ID == id {
			t.Done = true
			return nil
		}
	}
	return fmt.Errorf("todo %d not found", id)
}

func (tl *TodoList) Delete(id int) error {
	for i, t := range tl.todos {
		if t.ID == id {
			tl.todos = append(tl.todos[:i], tl.todos[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("todo %d not found", id)
}

func (tl *TodoList) Get(id int) (*Todo, error) {
	for _, t := range tl.todos {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("todo %d not found", id)
}

func (tl *TodoList) SetDueDate(id int, due time.Time) error {
	for _, t := range tl.todos {
		if t.ID == id {
			if due.Before(t.CreatedAt) {
				return fmt.Errorf("due date cannot be before creation date")
			}
			t.DueDate = due
			return nil
		}
	}
	return fmt.Errorf("todo %d not found", id)
}

func (tl *TodoList) Overdue() []*Todo {
	var result []*Todo
	now := time.Now()
	for _, t := range tl.todos {
		if t.DueDate.Before(now) {
			result = append(result, t)
		}
	}
	return result
}

func (tl *TodoList) ByPriority(p Priority) []*Todo {
	var result []*Todo
	for _, t := range tl.todos {
		if t.Priority == p && !t.Done {
			result = append(result, t)
		}
	}
	return result
}

func (tl *TodoList) ByTag(tag string) []*Todo {
	var result []*Todo
	for _, t := range tl.todos {
		for _, tt := range t.Tags {
			if tt == tag {
				result = append(result, t)
			}
		}
	}
	return result
}

func (tl *TodoList) Stats() (total int, done int, pending int) {
	for _, t := range tl.todos {
		total++
		if t.Done {
			done++
		}
	}
	pending = total - done
	return
}

func (tl *TodoList) Search(query string) []*Todo {
	var result []*Todo
	for _, t := range tl.todos {
		if strings.Contains(t.Title, query) {
			result = append(result, t)
		}
	}
	return result
}

func (tl *TodoList) Sorted() []*Todo {
	sorted := make([]*Todo, len(tl.todos))
	copy(sorted, tl.todos)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority > sorted[j].Priority
	})
	return sorted
}

func (tl *TodoList) Reopen(id int) error {
	for _, t := range tl.todos {
		if t.ID == id {
			t.Done = false
			return nil
		}
	}
	return fmt.Errorf("todo %d not found", id)
}

func (tl *TodoList) AddTag(id int, tag string) error {
	for _, t := range tl.todos {
		if t.ID == id {
			t.Tags = append(t.Tags, tag)
			return nil
		}
	}
	return fmt.Errorf("todo %d not found", id)
}

func (tl *TodoList) RemoveTag(id int, tag string) error {
	for _, t := range tl.todos {
		if t.ID == id {
			for j, tt := range t.Tags {
				if tt == tag {
					t.Tags = append(t.Tags[:j], t.Tags[j+1:]...)
					return nil
				}
			}
			return fmt.Errorf("tag %s not found", tag)
		}
	}
	return fmt.Errorf("todo %d not found", id)
}

func (tl *TodoList) SetPriority(id int, p Priority) error {
	for _, t := range tl.todos {
		if t.ID == id {
			t.Priority = p
			return nil
		}
	}
	return fmt.Errorf("todo %d not found", id)
}

func (tl *TodoList) Rename(id int, title string) error {
	for _, t := range tl.todos {
		if t.ID == id {
			t.Title = title
			return nil
		}
	}
	return fmt.Errorf("todo %d not found", id)
}

func (tl *TodoList) Pending() []*Todo {
	var result []*Todo
	for _, t := range tl.todos {
		if !t.Done {
			result = append(result, t)
		}
	}
	return result
}

func (tl *TodoList) Completed() []*Todo {
	var result []*Todo
	for _, t := range tl.todos {
		if t.Done {
			result = append(result, t)
		}
	}
	return result
}

func (tl *TodoList) ClearCompleted() int {
	var remaining []*Todo
	count := 0
	for _, t := range tl.todos {
		if t.Done {
			count++
		} else {
			remaining = append(remaining, t)
		}
	}
	tl.todos = remaining
	return count
}
