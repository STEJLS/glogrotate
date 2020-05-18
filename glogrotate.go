/*

Gzips and deletes log files generated by glog http://github.com/golang/glog

Basic usage:
	go run glogrotate.go -v=1 -base=/home/stejls/logs/ -maxsize=1 -maxinfoage=$((30*24))h -maxerrage=$((2*30*24))h reg/
*/
package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/golang/glog"
)

const (
	defaultDeleteInfoAfter  = 2 * 30 * 24 * time.Hour
	defaultDeleteErrorAfter = 6 * 30 * 24 * time.Hour
	defaultMaxFilesSize     = 2
	gb                      = 1 << 30
)

const (
	levelInfo    = "INFO"
	levelWarning = "WARNING"
	levelError   = "ERROR"
	levelFatal   = "FATAL"
)

var fileNameRE = regexp.MustCompile("\\d{8}-\\d{6}")

var (
	base            = flag.String("base", "/var/log/", "log subdir")
	deleteInfoAfter = flag.Duration("maxinfoage", defaultDeleteInfoAfter, "delete INFO files older than this")
	deleteErrAfter  = flag.Duration("maxerrage", defaultDeleteErrorAfter, "delete ERROR files older than this")
	maxSize         = flag.Uint64("maxsize", defaultMaxFilesSize, "delete oldest file if total size greater (in GB)")
)

func main() {
	flag.Set("logtostderr", "true")
	flag.Parse()

	for _, log := range flag.Args() {
		r := newRotater(filepath.Join(*base, log))
		r.Rotate()
		glog.Infof("Очистка '%s' закончена", log)
	}
}

func newRotater(dir string) *rotater {
	return &rotater{directory: dir,
		levels: []string{levelInfo, levelWarning, levelError, levelFatal},
	}
}

type rotater struct {
	directory string
	levels    []string
	files     map[string][]*fileInfo
	totalSize int64
}

func (r *rotater) Rotate() {
	if err := r.scanToLevels(r.directory); err != nil {
		glog.Infof("Error: %s", err)
		return
	}

	for _, lvl := range r.levels {
		if r.files[lvl] != nil {
			if err := r.clean(lvl); err != nil {
				glog.Infof("Error: %s", err)
				return
			}
		}
	}

	if err := r.applaySizeLimit(); err != nil {
		glog.Infof("Error: %s", err)
		return
	}
}

func (r *rotater) clean(lvl string) error {
	if glog.V(1) {
		glog.Infof("cleanning %s\n", lvl)
	}
	deleteAfter := r.deleteAfter(lvl)
	files := r.files[lvl]
	// Определим файлы которые трагать не будем
	var firstToDel int
	for i, f := range files {
		// Иначе, проверим что он сжат
		if !strings.HasSuffix(f.name, ".gz") {
			if glog.V(1) {
				glog.Infof("gzipping %s...\n", f)
			}
			if err := exec.Command("gzip", f.name).Run(); err != nil {
				glog.Infof("gzip: %s", err)
				continue
			}
		}

		// Если это файл на удаление значит мы дошли до "порога"
		if f.Creation().Before(time.Now().Add(-deleteAfter)) {
			firstToDel = i
			break

		}
	}

	// Нам нечего удалять
	if firstToDel == 0 || firstToDel+1 == len(files) {
		return nil
	}

	for i := firstToDel + 1; i < len(files); i++ {
		if glog.V(1) {
			glog.Infof("delete %s\n", files[i].name)
		}
		err := os.Remove(files[i].name)
		if err != nil {
			return err
		}
	}

	r.files[lvl] = files[0 : firstToDel+1]
	return nil
}

func (r *rotater) applaySizeLimit() error {
	if glog.V(1) {
		glog.Info("Applay size limit")
	}

	for {
		files, err := r.scan()
		if err != nil {
			return err
		}

		if len(files) == 0 {
			if glog.V(1) {
				glog.Infof("Nothing to delete")
			}
			return nil
		}

		if uint64(r.totalSize) < gb*(*maxSize) {
			if glog.V(1) {
				glog.Infof("delete by size not required. Total size:  %d bytes \n", r.totalSize)
			}
			break
		}

		if glog.V(1) {
			glog.Infof("delete by size required. Total size:  %d bytes \n", r.totalSize)
		}

		oldestFile := files[len(files)-1]
		if glog.V(1) {
			glog.Infof("delete oldest file %s \n", oldestFile)
		}

		err = os.Remove(oldestFile.name)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *rotater) scan() ([]*fileInfo, error) {
	r.totalSize = 0
	// Список файлов без симлинков и файлов на которые они указывают
	candidates := make(map[string]*fileInfo, 50)

	if glog.V(1) {
		glog.Infof("scan %s/*...\n", r.directory)
	}
	fileAndDirNames, err := filepath.Glob(r.directory + "/*")
	if err != nil {
		return nil, err
	}

	symlinked := make([]string, 0, 4)

	for _, f := range fileAndDirNames {
		info, err := os.Stat(f)
		if err != nil {
			if glog.V(1) {
				glog.Infof("Error while getting info about file -> "+f, err)
			}
			continue
		}
		if info.IsDir() {
			continue
		}
		if t, err := os.Readlink(f); err == nil {
			// it's a symlink to the current file.
			symlinked = append(symlinked, filepath.Join(filepath.Dir(f), t))
			continue
		}

		r.totalSize += info.Size()
		if !fileNameRE.MatchString(f) {
			if glog.V(1) {
				glog.Infof("Skeep file -> " + f)
			}
			continue
		}
		candidates[f] = &fileInfo{name: f}
	}

	for _, fileName := range symlinked {
		delete(candidates, fileName)
	}

	result := make([]*fileInfo, 0, len(candidates))
	for _, f := range candidates {
		result = append(result, f)
	}

	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Creation().After(result[j].Creation())
	})

	return result, nil
}

func (r *rotater) scanToLevels(dir string) error {
	sorted, err := r.scan()
	if err != nil {
		return err
	}

	r.files = make(map[string][]*fileInfo)

	for _, f := range sorted {
		lvl := f.level()
		r.files[lvl] = append(r.files[lvl], f)
	}

	return nil
}

func (r *rotater) deleteAfter(lvl string) time.Duration {
	var dAfter time.Duration

	switch lvl {
	case "INFO":
		dAfter = *deleteInfoAfter
	case "WARNING":
		dAfter = *deleteErrAfter
	case "ERROR", "FATAL":
		dAfter = *deleteErrAfter
	default:
		if glog.V(1) {
			glog.Infof("weird log level: %q\n", lvl)
		}
	}

	return dAfter
}

type fileInfo struct {
	name    string
	created time.Time
}

func (f *fileInfo) String() string {
	return f.name
}

// we want the date from 'one.rz-reqmngt1-eu.root.log.ERROR.20150320-103857.29198'
func (f *fileInfo) Creation() time.Time {
	fields := strings.Split(f.name, ".")
	if len(fields) < 3 {
		if glog.V(1) {
			glog.Infof("unexpected filename: %q \n", f.name)
		}
		return time.Time{}
	}
	if fields[len(fields)-1] == `gz` {
		fields = fields[:len(fields)-1]
	}
	d, err := time.Parse("20060102", strings.SplitN(fields[len(fields)-2], "-", 2)[0])
	if err != nil && glog.V(1) {
		glog.Infof("invalid date: %s", err)
		return time.Time{}
	}

	return d
}

func (f *fileInfo) level() string {
	fields := strings.Split(f.name, ".")
	if len(fields) < 3 {
		if glog.V(1) {
			glog.Infof("unexpected filename: %q \n", f.name)
		}
		return ""
	}
	if fields[len(fields)-1] == `gz` {
		fields = fields[:len(fields)-1]
	}

	if len(fields) < 3 {
		if glog.V(1) {
			glog.Infof("unexpected filename: %q \n", f.name)
		}
		return ""
	}

	return fields[len(fields)-3]
}
