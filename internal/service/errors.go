package service

import "fmt"

type NotFoundError struct {
	Entity string
	ID     string
}

func (e *NotFoundError) Error() string { return fmt.Sprintf("%s %s not found", e.Entity, e.ID) }

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error: %s — %s", e.Field, e.Message)
}

type TransitionError struct {
	From    string
	To      string
	Message string
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("cannot transition from %s to %s: %s", e.From, e.To, e.Message)
}
