package webgui

import "sync"

var nativeAttention struct {
	sync.RWMutex
	onCompleted func()
}

func registerNativeCompletion(fn func()) func() {
	nativeAttention.Lock()
	nativeAttention.onCompleted = fn
	nativeAttention.Unlock()
	return func() {
		nativeAttention.Lock()
		nativeAttention.onCompleted = nil
		nativeAttention.Unlock()
	}
}

func signalNativeRunCompleted() {
	nativeAttention.RLock()
	fn := nativeAttention.onCompleted
	nativeAttention.RUnlock()
	if fn != nil {
		fn()
	}
}
