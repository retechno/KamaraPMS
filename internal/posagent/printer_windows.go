//go:build windows

package posagent

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Windows spooler (winspool.drv) takes RAW documents: the bytes of ESC/POS go to the printer driver untouched. No cgo: the functions are
// called through x/sys/windows.
var (
	winspool         = windows.NewLazySystemDLL("winspool.drv")
	procOpenPrinter  = winspool.NewProc("OpenPrinterW")
	procClosePrinter = winspool.NewProc("ClosePrinter")
	procStartDoc     = winspool.NewProc("StartDocPrinterW")
	procEndDoc       = winspool.NewProc("EndDocPrinter")
	procStartPage    = winspool.NewProc("StartPagePrinter")
	procEndPage      = winspool.NewProc("EndPagePrinter")
	procWritePrinter = winspool.NewProc("WritePrinter")
)

type docInfo1 struct {
	DocName    *uint16
	OutputFile *uint16
	Datatype   *uint16
}

func openPrinter(name string) (windows.Handle, error) {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	var h windows.Handle
	r, _, e := procOpenPrinter.Call(uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(&h)), 0) //nolint:gosec // G103: the Win32 call needs the pointers
	if r == 0 {
		return 0, fmt.Errorf("the Windows printer %q cannot be opened: %w", name, e)
	}
	return h, nil
}

// writeSpooler prints data as a RAW document on the Windows printer called name.
func writeSpooler(name string, data []byte) error {
	h, err := openPrinter(name)
	if err != nil {
		return &PrintError{Code: CodeConnect, Err: err}
	}
	defer procClosePrinter.Call(uintptr(h))
	docName, _ := windows.UTF16PtrFromString("KamaraPMS")
	raw, _ := windows.UTF16PtrFromString("RAW")
	doc := docInfo1{DocName: docName, Datatype: raw}
	r, _, e := procStartDoc.Call(uintptr(h), 1, uintptr(unsafe.Pointer(&doc))) //nolint:gosec // G103: the Win32 call needs the pointers
	if r == 0 {
		return &PrintError{Code: CodeWrite, Err: fmt.Errorf("StartDocPrinter: %w", e)}
	}
	defer procEndDoc.Call(uintptr(h))
	if r, _, e := procStartPage.Call(uintptr(h)); r == 0 {
		return &PrintError{Code: CodeWrite, Err: fmt.Errorf("StartPagePrinter: %w", e)}
	}
	defer procEndPage.Call(uintptr(h))
	if len(data) == 0 {
		return &PrintError{Code: CodeWrite, Err: errors.New("nothing to print")}
	}
	var written uint32
	r, _, e = procWritePrinter.Call(uintptr(h), uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), uintptr(unsafe.Pointer(&written))) //nolint:gosec // G103: the Win32 call needs the pointers
	if r == 0 || int(written) != len(data) {
		return &PrintError{Code: CodeWrite, Err: fmt.Errorf("WritePrinter wrote %d of %d bytes: %w", written, len(data), e)}
	}
	return nil
}

// probeSpooler says whether the Windows printer can be opened.
func probeSpooler(name string) string {
	h, err := openPrinter(name)
	if err != nil {
		return StatusOffline
	}
	procClosePrinter.Call(uintptr(h))
	return StatusOnline
}
