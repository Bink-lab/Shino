//go:build windows

package tui

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	coinitApartmentThreaded = 0x2
	clsctxInprocServer      = 0x1
	sigdnFilesysPath        = 0x80058000
	hrChangedMode           = 0x80010106
	hresultCancelled        = 0x800704C7

	fosForceFileSystem = 0x40
	fosPathMustExist   = 0x800
	fosFileMustExist   = 0x1000
	fosPickFolders     = 0x20
	fosDontAddToRecent = 0x2000000
)

var (
	ole32                           = windows.NewLazySystemDLL("ole32.dll")
	shell32                         = windows.NewLazySystemDLL("shell32.dll")
	procCoInitializeEx              = ole32.NewProc("CoInitializeEx")
	procCoUninitialize              = ole32.NewProc("CoUninitialize")
	procCoCreateInstance            = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree               = ole32.NewProc("CoTaskMemFree")
	procSHCreateItemFromParsingName = shell32.NewProc("SHCreateItemFromParsingName")
)

type comPtr struct {
	p unsafe.Pointer
}

func (c comPtr) call(index int, args ...uintptr) uintptr {
	if c.p == nil {
		return 0x80004003
	}
	vtbl := *(*unsafe.Pointer)(c.p)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(index)*unsafe.Sizeof(uintptr(0))))
	r1, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(c.p)}, args...)...)
	return r1
}

func (c comPtr) release() {
	if c.p != nil {
		c.call(2)
	}
}

func hresultErr(hr uintptr, what string) error {
	if int32(hr) >= 0 {
		return nil
	}
	return fmt.Errorf("%s failed (0x%08x)", what, uint32(hr))
}

func utf16(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return nil
	}
	return p
}

type filterSpec struct {
	Name *uint16
	Spec *uint16
}

func pickFile(title, filterName, filterPattern, startDir string) (string, error) {
	specs := []filterSpec{
		{Name: utf16(filterName), Spec: utf16(filterPattern)},
		{Name: utf16("All files"), Spec: utf16("*.*")},
	}
	return showFileDialog(title, specs, false, startDir)
}

func pickFolder(title, startDir string) (string, error) {
	return showFileDialog(title, nil, true, startDir)
}

func showFileDialog(title string, specs []filterSpec, folder bool, startDir string) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	switch uint32(hr) {
	case 0, 1:
		defer procCoUninitialize.Call()
	case hrChangedMode:
	default:
		return "", fmt.Errorf("com init failed (0x%08x)", uint32(hr))
	}

	clsid, err := windows.GUIDFromString("{DC1C5A9C-E88A-4DDE-A5A1-60F82A20AEF7}")
	if err != nil {
		return "", err
	}
	iid, err := windows.GUIDFromString("{D57C7288-D4AD-4768-BE02-9D969532D960}")
	if err != nil {
		return "", err
	}
	var dialog unsafe.Pointer
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsid)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&dialog)))
	if dialog == nil {
		return "", hresultErr(hr, "create file dialog")
	}
	d := comPtr{p: dialog}
	defer d.release()

	var options uint32
	if err := hresultErr(d.call(10, uintptr(unsafe.Pointer(&options))), "get dialog options"); err != nil {
		return "", err
	}
	options |= fosForceFileSystem | fosPathMustExist | fosDontAddToRecent
	if folder {
		options |= fosPickFolders
	} else {
		options |= fosFileMustExist
	}
	if err := hresultErr(d.call(9, uintptr(options)), "set dialog options"); err != nil {
		return "", err
	}
	if startDir != "" {
		setDialogFolder(d, startDir)
	}
	if len(specs) > 0 {
		if err := hresultErr(d.call(4, uintptr(len(specs)), uintptr(unsafe.Pointer(&specs[0]))), "set file types"); err != nil {
			return "", err
		}
		runtime.KeepAlive(specs)
	}
	titlePtr := utf16(title)
	if err := hresultErr(d.call(17, uintptr(unsafe.Pointer(titlePtr))), "set title"); err != nil {
		return "", err
	}
	runtime.KeepAlive(titlePtr)

	hr = d.call(3, 0)
	if uint32(hr) == hresultCancelled {
		return "", nil
	}
	if err := hresultErr(hr, "show dialog"); err != nil {
		return "", err
	}

	var item unsafe.Pointer
	if err := hresultErr(d.call(20, uintptr(unsafe.Pointer(&item))), "get dialog result"); err != nil {
		return "", err
	}
	if item == nil {
		return "", nil
	}
	shellItem := comPtr{p: item}
	defer shellItem.release()

	var namePtr *uint16
	if err := hresultErr(shellItem.call(5, sigdnFilesysPath, uintptr(unsafe.Pointer(&namePtr))), "resolve selection path"); err != nil {
		return "", err
	}
	if namePtr == nil {
		return "", nil
	}
	path := windows.UTF16PtrToString(namePtr)
	procCoTaskMemFree.Call(uintptr(unsafe.Pointer(namePtr)))
	return path, nil
}

func setDialogFolder(d comPtr, dir string) {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return
	}
	iid, err := windows.GUIDFromString("{43826D1E-E718-42EE-BC55-A1E261C37BFE}")
	if err != nil {
		return
	}
	dirPtr := utf16(dir)
	var item unsafe.Pointer
	hr, _, _ := procSHCreateItemFromParsingName.Call(
		uintptr(unsafe.Pointer(dirPtr)), 0,
		uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&item)))
	if int32(hr) < 0 || item == nil {
		return
	}
	shellItem := comPtr{p: item}
	defer shellItem.release()
	d.call(12, uintptr(item))
}
