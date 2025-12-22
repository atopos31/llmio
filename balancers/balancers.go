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
	Success(key uint)
}

// 按权重概率抽取，类似抽签。
type Lottery struct {
	store   map[uint]int
	success uint
	fails   map[uint]struct{}
	reduces map[uint]struct{}
}

func NewLottery(items map[uint]int) *Lottery {
	return &Lottery{
		store:   items,
		fails:   map[uint]struct{}{},
		reduces: map[uint]struct{}{},
	}
}

func (w *Lottery) Pop() (uint, error) {
	if len(w.store) == 0 {
		return 0, fmt.Errorf("no provide items or all items are disabled")
	}
	total := 0
	for _, v := range w.store {
		total += v
	}
	if total <= 0 {
		return 0, fmt.Errorf("total provide weight must be greater than 0")
	}
	r := rand.IntN(total)
	for k, v := range w.store {
		if r < v {
			return k, nil
		}
		r -= v
	}
	return 0, fmt.Errorf("unexpected error")
}

func (w *Lottery) Delete(key uint) {
	w.fails[key] = struct{}{}
	delete(w.store, key)
}

func (w *Lottery) Reduce(key uint) {
	w.reduces[key] = struct{}{}
	w.store[key] -= w.store[key] / 3
}

func (w *Lottery) Success(key uint) {
	w.success = key
}

// 按顺序循环轮转，每次降低权重后移到队尾
type Rotor struct {
	*list.List
	success uint
	fails   map[uint]struct{}
	reduces map[uint]struct{}
}

func NewRotor(items map[uint]int) *Rotor {
	l := list.New()
	entries := lo.Entries(items)
	slices.SortFunc(entries, func(a lo.Entry[uint, int], b lo.Entry[uint, int]) int {
		return b.Value - a.Value
	})
	for _, entry := range entries {
		l.PushBack(entry.Key)
	}
	return &Rotor{
		List:    l,
		fails:   map[uint]struct{}{},
		reduces: map[uint]struct{}{},
	}
}

func (w *Rotor) Pop() (uint, error) {
	if w.Len() == 0 {
		return 0, fmt.Errorf("no provide items")
	}
	e := w.Front()
	return e.Value.(uint), nil
}

func (w *Rotor) Delete(key uint) {
	w.fails[key] = struct{}{}
	for e := w.Front(); e != nil; e = e.Next() {
		if e.Value.(uint) == key {
			w.Remove(e)
			return
		}
	}
}

func (w *Rotor) Reduce(key uint) {
	w.reduces[key] = struct{}{}
	for e := w.Front(); e != nil; e = e.Next() {
		if e.Value.(uint) == key {
			w.MoveToBack(e)
			return
		}
	}
}

func (w *Rotor) Success(key uint) {
	w.success = key
}
