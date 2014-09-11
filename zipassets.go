package zipassets

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
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

// deal with .tar.gz
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

// deal with .tar.bz2
func openTarBz2(za *ZipAssets) (err error) {
	var (
		f     *os.File
		tr    *tar.Reader
		hdr   *tar.Header
		bytes []byte
	)

	if f, err = os.Open(za.path); err != nil {
		return
	}
	defer f.Close()

	tr = tar.NewReader(bzip2.NewReader(f))

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

// todo: write header content-type
// serveHttp interface
func (za *ZipAssets) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	upath := req.URL.Path
	if strings.HasPrefix(upath, "/") {
		upath = upath[1:]
	}
	content, ok := za.files[upath]
	if !ok {
		http.NotFound(rw, req)
		return
	}
	// If Content-Type isn't set, use the file's extension to find it, but
	// if the Content-Type is unset explicitly, do not sniff the type.
	ctypes, haveType := rw.Header()["Content-Type"]
	var ctype string
	if !haveType {
		ctype = mime.TypeByExtension(filepath.Ext(upath))
		if ctype == "" {
			// read a chunk to decide between utf-8 text and binary
			const sniffLen = 512
			var (
				n   int
				buf []byte
			)
			if len(content) >= 512 {
				n = 512
			} else {
				n = len(content)
			}
			copy(buf, content[:n])
			ctype = http.DetectContentType(buf[:n])
		}
		rw.Header().Set("Content-Type", ctype)
	} else if len(ctypes) > 0 {
		ctype = ctypes[0]
	}

	rw.Write(content)
}

// modtime is the modification time of the resource to be served, or IsZero().
// return value is whether this request is now complete.
func checkLastModified(w http.ResponseWriter, r *http.Request, modtime time.Time) bool {
	if modtime.IsZero() {
		return false
	}

	// The Date-Modified header truncates sub-second precision, so
	// use mtime < t+1s instead of mtime <= t to check for unmodified.
	if t, err := time.Parse(http.TimeFormat, r.Header.Get("If-Modified-Since")); err == nil && modtime.Before(t.Add(1*time.Second)) {
		h := w.Header()
		delete(h, "Content-Type")
		delete(h, "Content-Length")
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	w.Header().Set("Last-Modified", modtime.UTC().Format(http.TimeFormat))
	return false
}

// checkETag implements If-None-Match and If-Range checks.
// The ETag must have been previously set in the ResponseWriter's headers.
//
// The return value is the effective request "Range" header to use and
// whether this request is now considered done.
func checkETag(w http.ResponseWriter, r *http.Request) (rangeReq string, done bool) {
	etag := w.Header().Get("Etag")
	rangeReq = r.Header.Get("Range")

	// Invalidate the range request if the entity doesn't match the one
	// the client was expecting.
	// "If-Range: version" means "ignore the Range: header unless version matches the
	// current file."
	// We only support ETag versions.
	// The caller must have set the ETag on the response already.
	if ir := r.Header.Get("If-Range"); ir != "" && ir != etag {
		// TODO(bradfitz): handle If-Range requests with Last-Modified
		// times instead of ETags? I'd rather not, at least for
		// now. That seems like a bug/compromise in the RFC 2616, and
		// I've never heard of anybody caring about that (yet).
		rangeReq = ""
	}

	if inm := r.Header.Get("If-None-Match"); inm != "" {
		// Must know ETag.
		if etag == "" {
			return rangeReq, false
		}

		// TODO(bradfitz): non-GET/HEAD requests require more work:
		// sending a different status code on matches, and
		// also can't use weak cache validators (those with a "W/
		// prefix).  But most users of ServeContent will be using
		// it on GET or HEAD, so only support those for now.
		if r.Method != "GET" && r.Method != "HEAD" {
			return rangeReq, false
		}

		// TODO(bradfitz): deal with comma-separated or multiple-valued
		// list of If-None-match values.  For now just handle the common
		// case of a single item.
		if inm == etag || inm == "*" {
			h := w.Header()
			delete(h, "Content-Type")
			delete(h, "Content-Length")
			w.WriteHeader(http.StatusNotModified)
			return "", true
		}
	}
	return rangeReq, false
}
