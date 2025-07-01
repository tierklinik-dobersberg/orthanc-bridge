package worklist

import (
	"bytes"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/dop251/goja"
	"github.com/fsnotify/fsnotify"
	"github.com/hashicorp/go-multierror"
	"github.com/suyashkumar/dicom"
	"github.com/suyashkumar/dicom/pkg/tag"
	customerv1 "github.com/tierklinik-dobersberg/apis/gen/go/tkd/customer/v1"
	dicomv1 "github.com/tierklinik-dobersberg/apis/gen/go/tkd/dicom/v1"
	orthanc_bridgev1 "github.com/tierklinik-dobersberg/apis/gen/go/tkd/orthanc_bridge/v1"
	"github.com/tierklinik-dobersberg/orthanc-bridge/internal/dicomweb"
)

type Worklist struct {
	targetDirectory string
	rulesDirectory  string
	rt              *goja.Runtime
	watcher         *fsnotify.Watcher

	onEntryCreate OnCreateCallback
	onEntryRemove OnRemoveCallback

	rules []rule
}

type OnCreateCallback func(string, dicom.Dataset)
type OnRemoveCallback func(string)

type rule struct {
	name string
	exec goja.Callable
}

func New(target, rulesDir string, onCreate OnCreateCallback, onRemove OnRemoveCallback) (*Worklist, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create inotify watcher: %w", err)
	}

	wl := &Worklist{
		targetDirectory: target,
		rulesDirectory:  rulesDir,
		watcher:         watcher,
		onEntryCreate:   onCreate,
		onEntryRemove:   onRemove,
	}

	if err := wl.initRuntime(); err != nil {
		return nil, fmt.Errorf("failed to initialize runtime: %w", err)
	}

	// verify target exists and is a directory
	stat, err := os.Stat(target)
	if err != nil {
		return nil, fmt.Errorf("failed to stat target: %w", err)
	}
	if !stat.IsDir() {
		return nil, fmt.Errorf("target %q is not a directory", target)
	}

	targetFs := os.DirFS(target)

	// read all rule files
	ruleFiles, err := fs.Glob(targetFs, "*.js")
	if err != nil {
		return nil, fmt.Errorf("failed to search for rules: %w", err)
	}
	rules := make(map[string]*goja.Program)
	for _, f := range ruleFiles {
		content, err := fs.ReadFile(targetFs, f)
		if err != nil {
			return nil, fmt.Errorf("failed to read rule file %q: %w", f, err)
		}

		p, err := goja.Compile(f, string(content), true)
		if err != nil {
			return nil, fmt.Errorf("failed to compile rule %q: %w", f, err)
		}

		rules[f] = p
	}

	// run all rule files
	for r, p := range rules {
		if _, err := wl.rt.RunProgram(p); err != nil {
			return nil, fmt.Errorf("failed to execute rule file %q: %w", r, err)
		}
	}

	// start watching rules and target directory
	wl.watcher.Add(target)
	wl.watcher.Add(rulesDir)

	go wl.watchFs()

	return wl, nil
}

func (wl *Worklist) readEntry(path string) (dicom.Dataset, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return dicom.Dataset{}, fmt.Errorf("failed to read worklist: %w", err)
	}

	ds, err := dicom.ParseUntilEOF(bytes.NewReader(content), nil, dicom.SkipPixelData())
	if err != nil {
		return dicom.Dataset{}, fmt.Errorf("failed to parse: %w", err)
	}

	return ds, nil
}

func (wl *Worklist) watchFs() {
	for e := range wl.watcher.Events {
		// Worklist event
		if strings.HasPrefix(e.Name, wl.targetDirectory) {
			switch {
			case strings.Contains(e.Op.String(), "CLOSE_WRITE"):
				if wl.onEntryCreate != nil {
					// read the entry and emit it to the callback
					ds, err := wl.readEntry(e.Name)
					if err != nil {
						slog.Error("failed to read dicom file", "file", e.Name, "error", err)
					} else {
						wl.onEntryCreate(e.Name, ds)
					}
				}

			case e.Has(fsnotify.Remove):
				if wl.onEntryRemove != nil {
					wl.onEntryRemove(e.Name)
				}
			}

		} else

		// Rules event
		if strings.HasPrefix(e.Name, wl.rulesDirectory) {
			switch {
			case e.Has(fsnotify.Remove):
				slog.Info("rule file has been removed", "file", e.Name)

			case strings.Contains(e.Op.String(), "CLOSE_WRITE"):
				slog.Info("rule has been written", "file", e.Name)
			}
		}
	}
}

type Entry struct {
	Path    string
	Dataset dicom.Dataset
}

func (e *Entry) ToProto() (*orthanc_bridgev1.WorklistEntry, error) {
	elements := make([]*dicomv1.Element, 0, len(e.Dataset.Elements))

	for _, el := range e.Dataset.Elements {
		pb, err := dicomv1.ElementProto(el)
		if err != nil {
			return nil, err
		}

		elements = append(elements, pb)
	}

	pb := &orthanc_bridgev1.WorklistEntry{
		Name:     e.Path,
		Elements: elements,
	}

	return pb, nil
}

func (wl *Worklist) ListEntries() ([]Entry, error) {
	slog.Info("searching for log entries", "target", wl.targetDirectory)

	files, err := fs.Glob(os.DirFS(wl.targetDirectory), "*.wl")
	if err != nil {
		return nil, fmt.Errorf("failed to glob worklist directory: %w", err)
	}

	entries := make([]Entry, 0, len(files))
	merr := new(multierror.Error)
	for _, f := range files {
		ds, err := wl.readEntry(filepath.Join(wl.targetDirectory, f))
		if err != nil {
			slog.Error("failed to read worklist entry", "path", f, "error", err)

			merr.Errors = append(merr.Errors, fmt.Errorf("failed to read worklist entry %q: %w", f, err))
			continue
		}

		entries = append(entries, Entry{
			Path:    f,
			Dataset: ds,
		})
	}

	return entries, merr.ErrorOrNil()
}

func (wl *Worklist) Generate(customer *customerv1.Customer, patient *customerv1.Patient, ds dicom.Dataset) (dicom.Dataset, error) {
	merr := new(multierror.Error)

	for _, rule := range wl.rules {
		this := wl.rt.ToValue(nil)

		args := []goja.Value{
			wl.rt.ToValue(customer),
			wl.rt.ToValue(patient),
			wl.rt.ToValue(ds),
		}

		result, err := rule.exec(this, args...)
		if err != nil {
			merr.Errors = append(merr.Errors, fmt.Errorf("%s: %w", rule.name, err))
			continue
		}

		if result != nil {
			var resultSet []*dicom.Element
			if err := wl.rt.ExportTo(result, resultSet); err != nil {
				merr.Errors = append(merr.Errors, fmt.Errorf("failed to parse rule result %q: %w", rule.name, err))
			}

			return dicom.Dataset{
				Elements: resultSet,
			}, merr.ErrorOrNil()
		}
	}

	return dicom.Dataset{}, merr.ErrorOrNil()
}

func (wl *Worklist) initRuntime() error {
	rt := goja.New()

	wl.rt = rt

	wl.rt.Set("rule", wl.registerRule)
	wl.rt.Set("tag", wl.tag)

	for name := range dicomweb.TagNames {
		t, err := findTag(name)
		if err != nil {
			slog.Error("failed to find tag", "name", name)
			continue
		}

		wl.rt.Set(name, t)
	}

	return nil
}

func (wl *Worklist) registerRule(name string, exec goja.Callable) {
	wl.rules = append(wl.rules, rule{
		name: name,
		exec: exec,
	})
}

func findTag(name string) (tag.Tag, error) {
	t, err := tag.FindByKeyword(name)
	if err == nil {
		return t.Tag, nil
	}

	t, err = tag.FindByName(name)
	if err != nil {
		return tag.Tag{}, err
	}

	return t.Tag, nil
}

func (wl *Worklist) tag(name string, value any) (*dicom.Element, error) {
	t, err := findTag(name)
	if err != nil {
		return nil, err
	}

	return dicom.NewElement(t, value)
}
