package api

import "errors"

var (
	ErrNotFound       = errors.New("not found")
	ErrAlreadyExists  = errors.New("already exists")
	ErrInvalidInput   = errors.New("invalid input")
	ErrNotInitialized = errors.New("repository not initialized; run init first or create directories")
	ErrScopeExists    = errors.New("scope already active")
)
