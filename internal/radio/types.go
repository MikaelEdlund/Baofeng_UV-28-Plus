package radio

// Logger receives human-readable protocol log lines.
type Logger func(format string, args ...any)

// Progress receives (done, total) block counts during read/write.
type Progress func(done, total int)

// ProgramMagic is the clone-protocol handshake string for this radio family
// (UV-28 Plus/Pro, UV-21 Pro, UV-32 Pro). It matches the vendor app's
// handshakeCode() for device family 16/19 and CHIRP's UV17ProGPS ident.
const ProgramMagic = "PROGRAMCOLORPROU"

// KnownIdents are model strings this protocol is known to serve.
var KnownIdents = []string{"UV28PRO", "UV21PRO", "UV32PRO", "AR152"}

// ScanName is the Bluetooth advertised name of the radio's BLE bridge.
const ScanName = "walkie-talkie"
