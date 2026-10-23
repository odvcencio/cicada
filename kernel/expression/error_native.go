//go:build !tinygo || !cicada_reactor

package expression

func validationError(message string) error { return Error(message) }
