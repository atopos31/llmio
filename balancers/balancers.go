package balancers

import (
	"fmt"
	"math/rand/v2"
)

type Balancer interface {
	Pop() (uint, error)
	Weight(key uint) int
	Delete(key uint)
	Reduce(key uint)
}

type WeightedRandom map[uint]int

func (w WeightedRandom) Pop() (uint, error) {
	if len(w) == 0 {
		return 0, fmt.Errorf("no provide items")
	}
	total := 0
	for _, v := range w {
		total += v
	}
	if total <= 0 {
		return 0, fmt.Errorf("total provide weight must be greater than 0")
	}
	r := rand.IntN(total)
	for k, v := range w {
		if r < v {
			return k, nil
		}
		r -= v
	}
	return 0, fmt.Errorf("unexpected error")
}

func (w WeightedRandom) Weight(key uint) int {
	return w[key]
}

func (w WeightedRandom) Delete(key uint) {
	delete(w, key)
}

func (w WeightedRandom) Reduce(key uint) {
	w[key] -= w[key] / 3
}
