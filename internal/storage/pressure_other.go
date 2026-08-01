//go:build !linux

package storage

func Inspect(string) Status {
	return Evaluate(0, 0)
}
