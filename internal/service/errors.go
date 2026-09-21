package service

import "errors"

var (
	ErrNotFound            = errors.New("not found")
	ErrConflict            = errors.New("conflict")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
	ErrExternalConflict    = errors.New("external transaction conflict")
	ErrForbidden           = errors.New("forbidden")
	ErrValidation          = errors.New("validation")
	ErrInboxConflict       = errors.New("inbox payload conflict")
	ErrWalletExists        = errors.New("wallet already exists")
)
