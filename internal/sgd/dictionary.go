package sgd

import (
	_ "embed"

	"github.com/yuu-biz/ims-practice/internal/diameter"
)

var (
	//go:embed sgd.xml
	sgdDictionaryXML []byte
	//go:embed s6c.xml
	s6cDictionaryXML []byte
)

var dictionary = diameter.NewDictionary(sgdDictionaryXML, s6cDictionaryXML)

// LoadDictionary adds the SGd and S6c applications to go-diameter's
// default dictionary. A process must call it before it builds or reads a
// message of this package.
func LoadDictionary() error { return dictionary.Load() }
