//go:build ignore

package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: release-archive <source-epoch> <binary> <archive>")
		os.Exit(2)
	}
	epoch, err := strconv.ParseInt(os.Args[1], 10, 64)
	if err != nil {
		fail(err)
	}
	binary, err := os.Open(os.Args[2])
	if err != nil {
		fail(err)
	}
	defer binary.Close()
	info, err := binary.Stat()
	if err != nil {
		fail(err)
	}
	archive, err := os.OpenFile(os.Args[3], os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		fail(err)
	}
	gzipWriter := gzip.NewWriter(archive)
	tarWriter := tar.NewWriter(gzipWriter)
	header := &tar.Header{
		Name: "git-remote-cloak", Mode: 0o755, Size: info.Size(),
		ModTime: time.Unix(epoch, 0).UTC(), Uid: 0, Gid: 0, Uname: "root", Gname: "root",
		Format: tar.FormatUSTAR,
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		fail(err)
	}
	if _, err := io.Copy(tarWriter, binary); err != nil {
		fail(err)
	}
	if err := tarWriter.Close(); err != nil {
		fail(err)
	}
	if err := gzipWriter.Close(); err != nil {
		fail(err)
	}
	if err := archive.Close(); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
