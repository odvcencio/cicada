//go:build !tinygo || !cicada_reactor

package cmd

func validationError(message string) error { return Error(message) }
