package zipassets

import (
	"testing"
)

func TestOpenZip(t *testing.T) {
	za := &ZipAssets{"./testdata/assets.zip", make(map[string][]byte)}
	err := openZip(za)
	if err != nil {
		t.Fatal(err)
	}
}
