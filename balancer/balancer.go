package balancer

import (
	"fmt"
	"math/rand"
)

func WeightedRandom[T comparable](items map[T]int) (*T, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("no provide items")
	}
	total := 0
	for _, v := range items {
		total += v
	}
	if total <= 0 {
		return nil, fmt.Errorf("total provide weight must be greater than 0")
	}
	r := rand.Intn(total)
	for k, v := range items {
		if r < v {
			return &k, nil
		}
		r -= v
	}
	return nil, fmt.Errorf("unexpected error")
}
