package zipassets

import (
	"testing"
)

func TestOpenZip(t *testing.T) {
	za := &ZipAssets{"./testdata/assets.zip", make(map[string]*filecontent)}
	err := openZip(za)
	if err != nil {
		t.Fatal(err)
	}
}

func TestNewZipAssets(t *testing.T) {
	hdl, err := NewZipAssets("./testdata/assets.zip", true)
}
