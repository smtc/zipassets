package zipassets

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type filecontent struct {
	name         string
	isDir        bool
	lastModified time.Time
	content      []byte
}

type ZipAssets struct {
	path  string
	files map[string]*filecontent
}

// open zip assets file
func NewZipAssets(path string) (za *ZipAssets, err error) {
	za = &ZipAssets{path, make(map[string]*filecontent)}
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
		f  *os.File
		tr *tar.Reader
		gr *gzip.Reader
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

	err = openTar(za, tr)

	return
}

// deal with .tar.bz2
func openTarBz2(za *ZipAssets) (err error) {
	var (
		f  *os.File
		tr *tar.Reader
	)

	if f, err = os.Open(za.path); err != nil {
		return
	}
	defer f.Close()

	tr = tar.NewReader(bzip2.NewReader(f))

	err = openTar(za, tr)

	return
}

func openTar(za *ZipAssets, tr *tar.Reader) (err error) {
	var (
		hdr *tar.Header
		fc  filecontent
	)

	for {
		if hdr, err = tr.Next(); err == io.EOF {
			break
		}
		if err != nil {
			return
		}
		if fc.content, err = ioutil.ReadAll(tr); err != nil {
			return
		}
		fc.name = hdr.Name
		fc.lastModified = hdr.ModTime
		fc.isDir = hdr.FileInfo().IsDir()
		za.files[hdr.Name] = &fc
	}

	return
}

// deal zip file
func openZip(za *ZipAssets) (err error) {
	var (
		bytes []byte
		rc    io.ReadCloser
		fc    filecontent
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
		fc.name = f.Name
		fc.lastModified = f.ModTime()
		fc.content = bytes
		za.files[f.Name] = &fc
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
	fc, ok := za.files[upath]
	if !ok {
		http.NotFound(rw, req)
		return
	}

	if checkLastModified(rw, req, fc.lastModified) {
		return
	}

	rangeReq, done := checkETag(rw, req)
	if done {
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
			if len(fc.content) >= 512 {
				n = 512
			} else {
				n = len(fc.content)
			}
			copy(buf, fc.content[:n])
			ctype = http.DetectContentType(buf[:n])
		}
	} else if len(ctypes) > 0 {
		ctype = ctypes[0]
	}

	rw.Header().Set("Content-Type", ctype)

	rw.Write(fc.content)
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

// httpRange specifies the byte range to be sent to the client.
type httpRange struct {
	start, length int64
}

func (r httpRange) contentRange(size int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", r.start, r.start+r.length-1, size)
}

func (r httpRange) mimeHeader(contentType string, size int64) textproto.MIMEHeader {
	return textproto.MIMEHeader{
		"Content-Range": {r.contentRange(size)},
		"Content-Type":  {contentType},
	}
}

// parseRange parses a Range header string as per RFC 2616.
func parseRange(s string, size int64) ([]httpRange, error) {
	if s == "" {
		return nil, nil // header not present
	}
	const b = "bytes="
	if !strings.HasPrefix(s, b) {
		return nil, errors.New("invalid range")
	}
	var ranges []httpRange
	for _, ra := range strings.Split(s[len(b):], ",") {
		ra = strings.TrimSpace(ra)
		if ra == "" {
			continue
		}
		i := strings.Index(ra, "-")
		if i < 0 {
			return nil, errors.New("invalid range")
		}
		start, end := strings.TrimSpace(ra[:i]), strings.TrimSpace(ra[i+1:])
		var r httpRange
		if start == "" {
			// If no start is specified, end specifies the
			// range start relative to the end of the file.
			i, err := strconv.ParseInt(end, 10, 64)
			if err != nil {
				return nil, errors.New("invalid range")
			}
			if i > size {
				i = size
			}
			r.start = size - i
			r.length = size - r.start
		} else {
			i, err := strconv.ParseInt(start, 10, 64)
			if err != nil || i > size || i < 0 {
				return nil, errors.New("invalid range")
			}
			r.start = i
			if end == "" {
				// If no end is specified, range extends to end of the file.
				r.length = size - r.start
			} else {
				i, err := strconv.ParseInt(end, 10, 64)
				if err != nil || r.start > i {
					return nil, errors.New("invalid range")
				}
				if i >= size {
					i = size - 1
				}
				r.length = i - r.start + 1
			}
		}
		ranges = append(ranges, r)
	}
	return ranges, nil
}
