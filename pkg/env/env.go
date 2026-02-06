package env

import (
	"os"
	"strconv"
)

func GetEnvWithDefault[T ~string | ~bool](key string, defaultValue T) T {
	envValue, ok := os.LookupEnv(key)
	if !ok {
		return defaultValue
	}
	switch any(defaultValue).(type) {
	case string:
		return any(envValue).(T)
	case bool:
		b, err := strconv.ParseBool(envValue)
		if err != nil {
			return defaultValue
		}
		return any(b).(T)
	default:
		return defaultValue
	}
}
