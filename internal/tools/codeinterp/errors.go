package codeinterp

import (
	"errors"
	"fmt"
)

var (
	ErrTimeout             = errors.New("codeinterp: timeout")
	ErrBudgetExceeded      = errors.New("codeinterp: budget exceeded")
	ErrTokenBudgetExceeded = errors.New("codeinterp: token budget exceeded")
	ErrConversion          = errors.New("codeinterp: conversion error")
)

type TimeoutError struct{ Msg string }

func (e TimeoutError) Error() string {
	if e.Msg == "" {
		return ErrTimeout.Error()
	}
	return fmt.Sprintf("%s: %s", ErrTimeout, e.Msg)
}
func (TimeoutError) Unwrap() error { return ErrTimeout }

type BudgetExceededError struct{ Msg string }

func (e BudgetExceededError) Error() string {
	if e.Msg == "" {
		return ErrBudgetExceeded.Error()
	}
	return fmt.Sprintf("%s: %s", ErrBudgetExceeded, e.Msg)
}
func (BudgetExceededError) Unwrap() error { return ErrBudgetExceeded }

type TokenBudgetExceededError struct{ Msg string }

func (e TokenBudgetExceededError) Error() string {
	if e.Msg == "" {
		return ErrTokenBudgetExceeded.Error()
	}
	return fmt.Sprintf("%s: %s", ErrTokenBudgetExceeded, e.Msg)
}
func (TokenBudgetExceededError) Unwrap() error { return ErrTokenBudgetExceeded }
