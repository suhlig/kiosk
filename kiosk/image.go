package kiosk

import "sync"

type Image struct {
	mutex sync.RWMutex
	data  []byte
}

func (d *Image) Store(data []byte) {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	d.data = data
}

func (d *Image) Get() []byte {
	d.mutex.RLock()
	defer d.mutex.RUnlock()

	return d.data
}
