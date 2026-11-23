//go:build !darwin && !windows

package profile

func localDataDirectory() (string, error) {
	return "", ErrLocation
}
