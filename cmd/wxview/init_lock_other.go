//go:build !darwin

package main

func lockInit() (func(), error) { return func() {}, nil }
