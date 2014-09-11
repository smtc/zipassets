package zipassets

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
)

type ZipAssets struct {
	path  string
	files map[string][]byte
}

// open zip assets file
func NewZipAssets(path string) (za *ZipAssets, err error) {
	za = &ZipAssets{path, make(map[string][]byte)}
	lowerPath := strings.ToLower(path)
	if strings.HasSuffix(lowerPath, "zip") {
		err = openZip(za)
	} else if strings.HasSuffix(lowerPath, "tar.gz") {
		err = openTarGz(za)
	} else if strings.HasSuffix(lowerPath, "tar.bz2") {
		err = openTarBz2(za)
	}

	return
}

func openTarBz2(za *ZipAssets) (err error) {
	return
}

func openTarGz(za *ZipAssets) (err error) {
	var (
		f     *os.File
		tr    *tar.Reader
		gr    *gzip.Reader
		hdr   *tar.Header
		bytes []byte
	)

	if f, err = os.Open(za.path); err != nil {
		return
	}
	defer f.Close()

	if gr, err = gzip.NewReader(f); err != nil {
		return
	}
	defer gr.Close()

	tr = tar.NewReader(gr)

	for {
		if hdr, err = tr.Next(); err == io.EOF {
			break
		}
		if err != nil {
			return
		}
		if bytes, err = ioutil.ReadAll(tr); err != nil {
			return
		}
		za.files[hdr.Name] = bytes
	}

	return
}

func openZip(za *ZipAssets) (err error) {
	var (
		bytes []byte
		rc    io.ReadCloser
	)

	r, err := zip.OpenReader(za.path)
	if err != nil {
		return
	}
	defer r.Close()

	// Iterate through the files in the archive,
	// printing some of their contents.
	for _, f := range r.File {
		fmt.Println(f.Name)
		rc, err = f.Open()
		if err != nil {
			return
		}
		bytes, err = ioutil.ReadAll(rc)
		if err != nil {
			return
		}
		za.files[f.Name] = bytes
	}

	return
}

// serveHttp interface
func (za *ZipAssets) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	upath := req.URL.Path
	if strings.HasPrefix(upath, "/") {
		upath = upath[1:]
	}
	if bytes, ok := za.files[upath]; ok {
		rw.Write(bytes)
	}
}
