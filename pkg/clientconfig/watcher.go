package clientconfig

import (
	"context"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

type EventOp string

var (
	Create EventOp = "Create"
	Remove EventOp = "Remove"
	Modify EventOp = "Modify"
	Rename EventOp = "Rename"
	Chmod  EventOp = "Chmod"
)

type Event struct {
	Name string
	Op   EventOp
}

type FileWatcher struct {
	watcher *fsnotify.Watcher
}

func (w *FileWatcher) Watch(ctx context.Context, path string) (<-chan *Event, error) {
	var err error
	w.watcher, err = fsnotify.NewWatcher()
	if err != nil {
		logrus.WithError(err).Errorf("failed to create watcher")
	}

	err = w.watcher.Add(path)
	if err != nil {
		logrus.WithError(err).Errorf("failed to open %q", path)
		return nil, err
	}

	ch := make(chan *Event)
	go func() {
		defer w.watcher.Close()
		for {
			select {
			case event, ok := <-w.watcher.Events:
				if !ok {
					return
				}
				e := &Event{
					Name: event.Name,
				}
				if event.Op&fsnotify.Create == fsnotify.Create {
					e.Op = Create
				} else if event.Op&fsnotify.Write == fsnotify.Write {
					e.Op = Modify
				} else if event.Op&fsnotify.Remove == fsnotify.Remove {
					e.Op = Remove
				} else if event.Op&fsnotify.Rename == fsnotify.Rename {
					e.Op = Rename
				} else if event.Op&fsnotify.Chmod == fsnotify.Chmod {
					e.Op = Chmod
				}
				ch <- e
			case err, ok := <-w.watcher.Errors:
				if !ok {
					return
				}
				logrus.WithError(err).Errorf("ifnotify error")
			case <-ctx.Done():
				break
			}
		}
	}()

	return ch, nil
}
