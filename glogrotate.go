/*
gzip/delete old files.

Usage:
	glogrotate -base=/var/log -maxage=240h one reqhandler decengine

Structure:

one.INFO -> one.rz-reqmngt1-eu.root.log.INFO.20150330-130800.28979
one.rz-reqmngt1-eu.root.log.INFO.20150320-103843.29198
one.rz-reqmngt1-eu.root.log.INFO.20150320-155154.27167
one.rz-reqmngt1-eu.root.log.INFO.20150323-140802.5388
one.rz-reqmngt1-eu.root.log.INFO.20150323-141237.7148
one.rz-reqmngt1-eu.root.log.INFO.20150323-145048.15374
one.rz-reqmngt1-eu.root.log.INFO.20150323-152106.22198
one.rz-reqmngt1-eu.root.log.INFO.20150325-163412.30922
one.rz-reqmngt1-eu.root.log.INFO.20150330-122311.17874
one.rz-reqmngt1-eu.root.log.INFO.20150330-125246.25200
one.rz-reqmngt1-eu.root.log.INFO.20150330-130800.28979

one.INFO and what it points to are always skipped.
*/
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultDeleteInfoAfter = 30 * 24 * time.Hour
	defaultWarnMult        = 2
	defaultErrorMult       = 3
)

var (
	base            = flag.String("base", "/var/log/", "log subdir")
	deleteInfoAfter = flag.Duration("maxage", defaultDeleteInfoAfter, "delete INFO files older than this")
	warnMult        = flag.Int("warn", defaultWarnMult, "multiplier relative to maxage for WARNING files")
	errorMult       = flag.Int("error", defaultErrorMult, "multiplier relative to maxage for ERROR/FATAL files")
)

func main() {
	flag.Parse()
	// fmt.Printf("Deleting after: %s\n", *deleteAfter)

	for _, log := range flag.Args() {
		clean(*base+"/"+log, log)
	}
}

func clean(dir, name string) {
	fmt.Printf("Clean %s/%s*...\n", dir, name)
	fs, err := filepath.Glob(dir + "/" + name + "*")
	if err != nil {
		panic(err)
	}

	doNotTouch := map[string]struct{}{}
	var candidates []string

	for _, f := range fs {
		if t, err := os.Readlink(f); err == nil {
			// it's a symlink to the current file.
			a := filepath.Join(filepath.Dir(f), t)
			doNotTouch[a] = struct{}{}
			continue
		}
		candidates = append(candidates, f)
	}

	for _, f := range candidates {
		if _, ok := doNotTouch[f]; ok {
			fmt.Printf("don't touch: %s\n", f)
			continue
		}
		// we want the date from 'one.rz-reqmngt1-eu.root.log.ERROR.20150320-103857.29198'
		// (might have a .gz suffix)
		fields := strings.Split(f, ".")
		if len(fields) < 2 {
			panic(fmt.Sprintf("weird filename: %q", fields))
		}
		if fields[len(fields)-1] == `gz` {
			fields = fields[:len(fields)-1]
		}
		var dAfter time.Duration
		level := fields[len(fields)-3]
		switch level {
		case "INFO":
			dAfter = *deleteInfoAfter
		case "WARNING":
			dAfter = time.Duration(*warnMult) * (*deleteInfoAfter)
		case "ERROR", "FATAL":
			dAfter = time.Duration(*errorMult) * (*deleteInfoAfter)
		default:
			panic(fmt.Sprintf("weird log level: %q", level))
		}
		d, err := time.Parse("20060102", strings.SplitN(fields[len(fields)-2], "-", 2)[0])
		if err != nil {
			panic(err)
		}
		// fmt.Printf("%q: %s\n", f, d)
		if d.Before(time.Now().Add(-dAfter)) {
			fmt.Printf("delete %s\n", f)
			os.Remove(f)
			continue
		}
		if !strings.HasSuffix(f, ".gz") {
			fmt.Printf("Gzipping %s...\n", f)
			if err := exec.Command("gzip", f).Run(); err != nil {
				panic(err)
			}
		}
	}
}
