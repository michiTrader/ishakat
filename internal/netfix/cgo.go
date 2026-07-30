//go:build cgo

package netfix

// CGOEnabled indica si el binario se compiló con CGO. En Android es la
// diferencia entre tener DNS y no tenerlo.
const CGOEnabled = true
