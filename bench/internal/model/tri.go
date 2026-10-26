package model

// Tri は 真 / 偽 / 不明 の 3 値。並行する書き込みのせいで、読み取りの時点でどちらとも
// 決められない条件を不明として扱う。
type Tri int8

const (
	False Tri = iota
	Unknown
	True
)

func (t Tri) String() string {
	return [...]string{"false", "unknown", "true"}[t]
}

func triOf(b bool) Tri {
	if b {
		return True
	}
	return False
}

// And は Kleene の論理積。偽が 1 つでもあれば偽。
func And(values ...Tri) Tri {
	result := True
	for _, v := range values {
		result = min(result, v)
	}
	return result
}

// Or は Kleene の論理和。真が 1 つでもあれば真。
func Or(values ...Tri) Tri {
	result := False
	for _, v := range values {
		result = max(result, v)
	}
	return result
}

func Not(t Tri) Tri {
	return True - t
}

// forAll は、読み取り r で tl が取りうる値ごとに求めた値をまとめる。すべて真なら真、すべて偽なら偽。
func forAll[T comparable](tl *timeline[T], r Read, f func(T) Tri) Tri {
	seenTrue, seenFalse := false, false
	tl.each(r, func(state T) {
		if seenTrue && seenFalse {
			return
		}
		switch f(state) {
		case True:
			seenTrue = true
		case False:
			seenFalse = true
		default:
			seenTrue, seenFalse = true, true
		}
	})
	switch {
	case seenTrue && !seenFalse:
		return True
	case seenFalse && !seenTrue:
		return False
	default:
		return Unknown
	}
}
