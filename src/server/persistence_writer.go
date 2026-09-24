package main

import (
	"errors"
	"sync"
)

var errPersistenceWriterClosed = errors.New("persistence writer is closed")

type persistenceRequest struct {
	storeFn func(*jobStore) error
	done    chan error
}

type persistenceWriter struct {
	store  *jobStore
	mu     sync.Mutex
	ready  *sync.Cond
	queue  []persistenceRequest
	closed bool
	done   chan struct{}
}

func newPersistenceWriter(store *jobStore) *persistenceWriter {
	w := &persistenceWriter{store: store, done: make(chan struct{})}
	w.ready = sync.NewCond(&w.mu)
	go w.run()
	return w
}

func (w *persistenceWriter) enqueue(storeFn func(*jobStore) error) <-chan error {
	done := make(chan error, 1)
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		done <- errPersistenceWriterClosed
		return done
	}
	w.queue = append(w.queue, persistenceRequest{storeFn: storeFn, done: done})
	w.ready.Signal()
	w.mu.Unlock()
	return done
}

func (w *persistenceWriter) run() {
	defer close(w.done)
	for {
		w.mu.Lock()
		for len(w.queue) == 0 && !w.closed {
			w.ready.Wait()
		}
		if len(w.queue) == 0 && w.closed {
			w.mu.Unlock()
			return
		}
		request := w.queue[0]
		w.queue[0] = persistenceRequest{}
		w.queue = w.queue[1:]
		w.mu.Unlock()
		request.done <- request.storeFn(w.store)
	}
}

func (w *persistenceWriter) close() {
	w.mu.Lock()
	w.closed = true
	w.ready.Broadcast()
	w.mu.Unlock()
	<-w.done
}

func cloneJobStateForPersistence(source *jobState) *jobState {
	clone := *source
	clone.Job = source.Job
	clone.Job.Files = cloneMediaFiles(source.Job.Files)
	clone.Job.Items = cloneQueueItems(source.Job.Items)
	clone.Job.Failures = append([]itemFailure(nil), source.Job.Failures...)
	if source.Job.Progress != nil {
		value := *source.Job.Progress
		clone.Job.Progress = &value
	}
	if source.Job.TotalCount != nil {
		value := *source.Job.TotalCount
		clone.Job.TotalCount = &value
	}
	clone.fileItems = make(map[int]mediaFile, len(source.fileItems))
	for index, file := range source.fileItems {
		clone.fileItems[index] = cloneMediaFile(file)
	}
	clone.fileGroups = make(map[int][]mediaFile, len(source.fileGroups))
	for index, files := range source.fileGroups {
		clone.fileGroups[index] = cloneMediaFiles(files)
	}
	if source.librarySignatures != nil {
		clone.librarySignatures = make(map[string][32]byte, len(source.librarySignatures))
		for fileID, signature := range source.librarySignatures {
			clone.librarySignatures[fileID] = signature
		}
	}
	return &clone
}

func cloneQueueItems(items []queueItem) []queueItem {
	clone := append([]queueItem(nil), items...)
	for index := range clone {
		clone[index].FileIDs = append([]string(nil), items[index].FileIDs...)
		if items[index].Progress != nil {
			value := *items[index].Progress
			clone[index].Progress = &value
		}
	}
	return clone
}

func cloneMediaFiles(files []mediaFile) []mediaFile {
	clone := make([]mediaFile, len(files))
	for index, file := range files {
		clone[index] = cloneMediaFile(file)
	}
	return clone
}

func cloneMediaFile(file mediaFile) mediaFile {
	if file.Subtitle != nil {
		subtitle := *file.Subtitle
		file.Subtitle = &subtitle
	}
	file.Chapters = append([]mediaChapter(nil), file.Chapters...)
	return file
}
