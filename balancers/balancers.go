package balancers

import (
	"container/list"
	"fmt"
	"math/rand/v2"
	"slices"

	"github.com/samber/lo"
)

type Balancer interface {
	Pop() (uint, error)
	Delete(key uint)
	Reduce(key uint)
}

type WeightedRandom map[uint]int

func NewWeightedRandom(items map[uint]int) Balancer {
	return WeightedRandom(items)
}

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

func (w WeightedRandom) Delete(key uint) {
	delete(w, key)
}

func (w WeightedRandom) Reduce(key uint) {
	w[key] -= w[key] / 3
}

type WeightedList struct{ *list.List }

func NewWeightedList(items map[uint]int) WeightedList {
	l := list.New()
	entries := lo.Entries(items)
	slices.SortFunc(entries, func(a lo.Entry[uint, int], b lo.Entry[uint, int]) int {
		return b.Value - a.Value
	})
	for _, entry := range entries {
		l.PushBack(entry.Key)
	}
	return WeightedList{l}
}

func (w WeightedList) Pop() (uint, error) {
	if w.Len() == 0 {
		return 0, fmt.Errorf("no provide items")
	}
	e := w.Front()
	return e.Value.(uint), nil
}

func (w WeightedList) Delete(key uint) {
	for e := w.Front(); e != nil; e = e.Next() {
		if e.Value.(uint) == key {
			w.Remove(e)
			return
		}
	}
}

func (w WeightedList) Reduce(key uint) {
	for e := w.Front(); e != nil; e = e.Next() {
		if e.Value.(uint) == key {
			w.MoveToBack(e)
			return
		}
	}
}
