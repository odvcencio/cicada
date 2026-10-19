//go:build !tinygo || !cicada_reactor

package acid

func validationError(message string) error { return Error(message) }
