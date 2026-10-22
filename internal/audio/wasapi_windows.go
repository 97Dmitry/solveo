package audio

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Narrow WASAPI bindings: default render endpoint in loopback mode or default capture endpoint, shared mode,
// the endpoint's mix format, polled every 10 ms on a locked, COM-initialized thread.

var (
	ole32                 = windows.NewLazySystemDLL("ole32.dll")
	procCoCreateInstance  = ole32.NewProc("CoCreateInstance")
	procPropVariantClear  = ole32.NewProc("PropVariantClear")
	clsidDeviceEnumerator = windows.GUID{Data1: 0xBCDE0395, Data2: 0xE52F, Data3: 0x467C, Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidDeviceEnumerator   = windows.GUID{Data1: 0xA95664D2, Data2: 0x9614, Data3: 0x4F35, Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidAudioClient        = windows.GUID{Data1: 0x1CB9AD4C, Data2: 0xDBFA, Data3: 0x4C32, Data4: [8]byte{0xB1, 0x78, 0xC2, 0xF5, 0x68, 0xA7, 0x03, 0xB2}}
	iidAudioCaptureClient = windows.GUID{Data1: 0xC8ADBD64, Data2: 0xE71E, Data3: 0x48A0, Data4: [8]byte{0xA4, 0xDE, 0x18, 0x5C, 0x39, 0x5C, 0xD3, 0x17}}
	subtypeFloat          = windows.GUID{Data1: 0x00000003, Data2: 0x0000, Data3: 0x0010, Data4: [8]byte{0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71}}
	subtypePCM            = windows.GUID{Data1: 0x00000001, Data2: 0x0000, Data3: 0x0010, Data4: [8]byte{0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71}}
	pkeyFriendlyName      = propertyKey{FmtID: windows.GUID{Data1: 0xA45C254E, Data2: 0xDF1C, Data3: 0x4EFD, Data4: [8]byte{0x80, 0x20, 0x67, 0xD1, 0x46, 0xA8, 0x50, 0xE0}}, PID: 14}
)

const (
	eRender                  = 0
	eCapture                 = 1
	eConsole                 = 0
	clsctxAll                = 0x17
	stgmRead                 = 0
	sharemodeShared          = 0
	streamflagsLoopback      = 0x00020000
	bufferflagsSilent        = 0x2
	bufferDuration100ns      = 1_000_000 // 100 ms
	waveFormatPCM            = 1
	waveFormatIEEEFloat      = 3
	waveFormatTagExtensible  = 0xFFFE
	vtLPWSTR                 = 31
	hresultNotFound          = 0x80070490
	hresultDeviceInvalidated = 0x88890004
	pollInterval             = 10 * time.Millisecond
	vtblRelease              = 2
	enumGetDefaultEndpoint   = 4
	deviceActivate           = 3
	deviceOpenPropertyStore  = 4
	propertyStoreGetValue    = 5
	clientInitialize         = 3
	clientGetMixFormat       = 8
	clientStart              = 10
	clientStop               = 11
	clientGetService         = 14
	captureGetBuffer         = 3
	captureReleaseBuffer     = 4
	captureGetNextPacketSize = 5
)

type propertyKey struct {
	FmtID windows.GUID
	PID   uint32
}

type propVariant struct {
	vt       uint16
	reserved [3]uint16
	val      unsafe.Pointer
	pad      uintptr
}

type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Size           uint16
}

// WAVEFORMATEXTENSIBLE.SubFormat sits at byte 24: the C header packs WAVEFORMATEX to 18 bytes, Go pads it to 20.
const subFormatOffset = 24

func hresult(r uintptr, what string) error {
	if int32(r) >= 0 {
		return nil
	}
	switch uint32(r) {
	case hresultNotFound:
		return ErrNoDevice
	case hresultDeviceInvalidated:
		return errors.New("устройство звука отключено или изменено")
	}
	return fmt.Errorf("%s: HRESULT 0x%08X", what, uint32(r))
}

// call invokes method index of a COM object's vtable. uintptrescapes keeps every &local passed as uintptr
// on the heap and alive for the call, as in windows.LazyProc.Call: a moved goroutine stack must never be
// written by COM.
//
//go:uintptrescapes
func call(obj unsafe.Pointer, index int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, index*int(unsafe.Sizeof(uintptr(0)))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return r
}

func release(obj unsafe.Pointer) {
	if obj != nil {
		call(obj, vtblRelease)
	}
}

type wasapi struct{}

// NewCapturer returns the WASAPI capturer.
func NewCapturer() Capturer { return wasapi{} }

type session struct {
	src      Source
	enum     unsafe.Pointer
	device   unsafe.Pointer
	client   unsafe.Pointer
	capture  unsafe.Pointer
	format   sampleFormat
	rate     int
	channels int
	block    int
}

func (s *session) close() {
	if s.client != nil {
		call(s.client, clientStop)
	}
	release(s.capture)
	release(s.client)
	release(s.device)
	release(s.enum)
}

func (s *session) open() (Device, error) {
	r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidDeviceEnumerator)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidDeviceEnumerator)), uintptr(unsafe.Pointer(&s.enum)))
	if err := hresult(r, "CoCreateInstance(MMDeviceEnumerator)"); err != nil {
		return Device{}, err
	}
	flow := uintptr(eCapture)
	if s.src == System {
		flow = eRender
	}
	if err := hresult(call(s.enum, enumGetDefaultEndpoint, flow, eConsole, uintptr(unsafe.Pointer(&s.device))), "GetDefaultAudioEndpoint"); err != nil {
		return Device{}, err
	}
	name := s.friendlyName()
	if err := hresult(call(s.device, deviceActivate, uintptr(unsafe.Pointer(&iidAudioClient)), clsctxAll, 0, uintptr(unsafe.Pointer(&s.client))), "IMMDevice.Activate"); err != nil {
		return Device{}, err
	}
	var mix *waveFormatEx
	if err := hresult(call(s.client, clientGetMixFormat, uintptr(unsafe.Pointer(&mix))), "GetMixFormat"); err != nil {
		return Device{}, err
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(mix))
	if err := s.readFormat(mix); err != nil {
		return Device{}, err
	}
	flags := uintptr(0)
	if s.src == System {
		flags = streamflagsLoopback
	}
	if err := hresult(call(s.client, clientInitialize, sharemodeShared, flags, bufferDuration100ns, 0, uintptr(unsafe.Pointer(mix)), 0), "IAudioClient.Initialize"); err != nil {
		return Device{}, err
	}
	if err := hresult(call(s.client, clientGetService, uintptr(unsafe.Pointer(&iidAudioCaptureClient)), uintptr(unsafe.Pointer(&s.capture))), "GetService(IAudioCaptureClient)"); err != nil {
		return Device{}, err
	}
	if err := hresult(call(s.client, clientStart), "IAudioClient.Start"); err != nil {
		return Device{}, err
	}
	return Device{Name: name}, nil
}

func (s *session) readFormat(f *waveFormatEx) error {
	s.rate, s.channels, s.block = int(f.SamplesPerSec), int(f.Channels), int(f.BlockAlign)
	tag := f.FormatTag
	if tag == waveFormatTagExtensible {
		switch *(*windows.GUID)(unsafe.Add(unsafe.Pointer(f), subFormatOffset)) {
		case subtypeFloat:
			tag = waveFormatIEEEFloat
		case subtypePCM:
			tag = waveFormatPCM
		}
	}
	switch {
	case tag == waveFormatIEEEFloat && f.BitsPerSample == 32:
		s.format = formatFloat32
	case tag == waveFormatPCM && f.BitsPerSample == 16:
		s.format = formatPCM16
	default:
		return fmt.Errorf("%w: тег %d, %d бит", errUnsupportedFormat, f.FormatTag, f.BitsPerSample)
	}
	if s.rate <= 0 || s.channels <= 0 || s.block != s.channels*int(f.BitsPerSample)/8 {
		return errUnsupportedFormat
	}
	return nil
}

func (s *session) friendlyName() string {
	var store unsafe.Pointer
	if hresult(call(s.device, deviceOpenPropertyStore, stgmRead, uintptr(unsafe.Pointer(&store))), "OpenPropertyStore") != nil {
		return ""
	}
	defer release(store)
	var pv propVariant
	if hresult(call(store, propertyStoreGetValue, uintptr(unsafe.Pointer(&pkeyFriendlyName)), uintptr(unsafe.Pointer(&pv))), "GetValue") != nil {
		return ""
	}
	defer func() { _, _, _ = procPropVariantClear.Call(uintptr(unsafe.Pointer(&pv))) }()
	if pv.vt != vtLPWSTR || pv.val == nil {
		return ""
	}
	return windows.UTF16PtrToString((*uint16)(pv.val))
}

// drain reads every pending packet; it returns false when the stream failed.
func (s *session) drain(out *framer, filler *silenceFiller) error {
	for {
		var frames uint32
		if err := hresult(call(s.capture, captureGetNextPacketSize, uintptr(unsafe.Pointer(&frames))), "GetNextPacketSize"); err != nil || frames == 0 {
			return err
		}
		var data *byte
		var flags uint32
		if err := hresult(call(s.capture, captureGetBuffer, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&frames)), uintptr(unsafe.Pointer(&flags)), 0, 0), "GetBuffer"); err != nil {
			return err
		}
		var samples []float32
		var convErr error
		if flags&bufferflagsSilent != 0 || data == nil {
			samples = make([]float32, int(frames)*s.channels)
		} else {
			samples, convErr = convert(unsafe.Slice(data, int(frames)*s.block), s.format)
		}
		if err := hresult(call(s.capture, captureReleaseBuffer, uintptr(frames)), "ReleaseBuffer"); err != nil {
			return err
		}
		if convErr != nil {
			return convErr
		}
		filler.packet(time.Now())
		out.push(samples)
	}
}

func (wasapi) Start(ctx context.Context, src Source, sink func(Frame), fail func(error)) (Device, func(), error) {
	type opened struct {
		dev Device
		err error
	}
	ready := make(chan opened, 1)
	done := make(chan struct{})
	runCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil {
			ready <- opened{err: fmt.Errorf("CoInitializeEx: %w", err)}
			return
		}
		defer windows.CoUninitialize()
		s := &session{src: src}
		defer s.close()
		dev, err := s.open()
		ready <- opened{dev, err}
		if err != nil {
			return
		}
		out := &framer{rate: s.rate, channels: s.channels, sink: sink}
		filler := &silenceFiller{rate: s.rate, channels: s.channels}
		filler.packet(time.Now())
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
			}
			if err := s.drain(out, filler); err != nil {
				fail(err)
				return
			}
			if src == System {
				if zeros := filler.fill(time.Now()); len(zeros) > 0 {
					out.push(zeros)
				}
			}
		}
	}()
	res := <-ready
	stop := func() {
		cancel()
		<-done
	}
	if res.err != nil {
		stop()
		return Device{}, nil, res.err
	}
	return res.dev, stop, nil
}
