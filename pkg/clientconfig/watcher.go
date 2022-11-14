// Copyright 2016--2022 Lightbits Labs Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// you may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
