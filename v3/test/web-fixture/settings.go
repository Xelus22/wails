package main

import "errors"

// Settings is a bound service with the classic shapes: results, errors, args.
type Settings struct{}

func (s *Settings) GetTheme() string { return "dark" }

func (s *Settings) SetName(name string) (string, error) { return "hello " + name, nil }

func (s *Settings) Fail() (string, error) { return "", errors.New("boom") }
