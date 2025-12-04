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

// 按权重概率抽取，类似抽签。
type Lottery map[uint]int

func NewLottery(items map[uint]int) Balancer {
	return Lottery(items)
}

func (w Lottery) Pop() (uint, error) {
	if len(w) == 0 {
		return 0, fmt.Errorf("no provide items or all items are disabled")
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

func (w Lottery) Delete(key uint) {
	delete(w, key)
}

func (w Lottery) Reduce(key uint) {
	w[key] -= w[key] / 3
}

// 按顺序循环轮转，每次降低权重后移到队尾
type Rotor struct{ *list.List }

func NewRotor(items map[uint]int) Rotor {
	l := list.New()
	entries := lo.Entries(items)
	slices.SortFunc(entries, func(a lo.Entry[uint, int], b lo.Entry[uint, int]) int {
		return b.Value - a.Value
	})
	for _, entry := range entries {
		l.PushBack(entry.Key)
	}
	return Rotor{l}
}

func (w Rotor) Pop() (uint, error) {
	if w.Len() == 0 {
		return 0, fmt.Errorf("no provide items")
	}
	e := w.Front()
	return e.Value.(uint), nil
}

func (w Rotor) Delete(key uint) {
	for e := w.Front(); e != nil; e = e.Next() {
		if e.Value.(uint) == key {
			w.Remove(e)
			return
		}
	}
}

func (w Rotor) Reduce(key uint) {
	for e := w.Front(); e != nil; e = e.Next() {
		if e.Value.(uint) == key {
			w.MoveToBack(e)
			return
		}
	}
}
