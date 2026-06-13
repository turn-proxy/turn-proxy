package bufpool

import "sync"

const pooledSize = 2048

var pool = sync.Pool{New: func() any {
	return new(make([]byte, pooledSize))
}}

func Get(n int) *[]byte {
	if n > pooledSize {
		return new(make([]byte, n))
	}
	bp := pool.Get().(*[]byte)
	*bp = (*bp)[:n]
	return bp
}

func Put(bp *[]byte) {
	if bp == nil || cap(*bp) != pooledSize {
		return
	}
	pool.Put(bp)
}
