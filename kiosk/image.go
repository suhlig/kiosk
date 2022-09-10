package kiosk

import "sync"

type Image struct {
	id    string
	mutex sync.RWMutex
	data  []byte
}

func (i *Image) Store(id string, data []byte) {
	i.mutex.Lock()
	defer i.mutex.Unlock()

	i.id = id
	i.data = data
}

func (i *Image) GetData() []byte {
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	return i.data
}

func (i *Image) GetID() string {
	i.mutex.RLock()
	defer i.mutex.RUnlock()

	return i.id
}
