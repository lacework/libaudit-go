package libaudit

import (
	"fmt"
	"strconv"
	"syscall"
	"unsafe"

	"github.com/pkg/errors"
)

// EventCallback is the function signature for any function that wants to receive an AuditEvent as soon as
// it is received from the kernel. Error will be set to indicate any error that happens while receiving
// messages.
type EventCallback func(*AuditEvent, error, ...interface{})

// RawEventCallback is similar to EventCallback and provides a function signature but the difference is that the function
// will receive only the message string which contains the audit event and not the parsed AuditEvent struct.
type RawEventCallback func(string, error, ...interface{})
type RawEventTypeCallback func(uint16, string, error, ...interface{})

// AuditEvent holds a parsed audit message.
// Serial holds the serial number for the message.
// Timestamp holds the unix timestamp of the message.
// Type indicates the type of the audit message.
// Data holds a map of field values of audit messages where keys => field names and values => field values.
// Raw string holds the original audit message received from kernel.
type AuditEvent struct {
	Serial    string
	Timestamp string
	Type      string
	Data      map[string]string
	Raw       string
}

//NewAuditEvent takes a NetlinkMessage passed from the netlink connection
//and parses the data from the message header to return an AuditEvent struct.
func NewAuditEvent(msg NetlinkMessage) (*AuditEvent, error) {
	x, err := ParseAuditEvent(string(msg.Data[:]), auditConstant(msg.Header.Type), true)
	if err != nil {
		return nil, err
	}
	if (*x).Type == "auditConstant("+strconv.Itoa(int(msg.Header.Type))+")" {
		return nil, fmt.Errorf("NewAuditEvent failed: unknown message type %d", msg.Header.Type)
	}

	return x, nil
}

// GetAuditEvents receives audit messages from the kernel and parses them to AuditEvent struct.
// It passes them along the callback function and if any error occurs while receiving the message,
// the same will be passed in the callback as well.
// Code that receives the message runs inside a go-routine.
func GetAuditEvents(s Netlink, cb EventCallback, args ...interface{}) {
	go func() {
		rb := make([]byte, syscall.NLMSG_HDRLEN+MAX_AUDIT_MESSAGE_LENGTH)

		for {
			select {
			default:
				msgs, err := s.Receive(syscall.NLMSG_HDRLEN+MAX_AUDIT_MESSAGE_LENGTH, 0, rb)
				if err == nil {
					for _, msg := range msgs {
						if msg.Header.Type == syscall.NLMSG_ERROR {
							err := int32(nativeEndian().Uint32(msg.Data[0:4]))
							if err != 0 {
								cb(nil, fmt.Errorf("error receiving events %d", err), args...)
							}
						} else {
							nae, err := NewAuditEvent(msg)
							cb(nae, err, args...)
						}
					}
				}
			}
		}
	}()
}

// GetRawAuditEvents receives raw audit messages from kernel parses them to AuditEvent struct.
// It passes them along the callback function and if any error occurs while receiving the message,
// the same will be passed in the callback as well.
// Code that receives the message runs inside a go-routine.
func GetRawAuditEvents(s Netlink, cb RawEventCallback, args ...interface{}) {
	go func() {
		rb := make([]byte, syscall.NLMSG_HDRLEN+MAX_AUDIT_MESSAGE_LENGTH)

		for {
			select {
			default:
				msgs, err := s.Receive(syscall.NLMSG_HDRLEN+MAX_AUDIT_MESSAGE_LENGTH, 0, rb)
				if err == nil {
					for _, msg := range msgs {
						var (
							m   string
							err error
						)
						if msg.Header.Type == syscall.NLMSG_ERROR {
							v := int32(nativeEndian().Uint32(msg.Data[0:4]))
							if v != 0 {
								cb(m, fmt.Errorf("error receiving events %d", v), args...)
							}
						} else {
							Type := auditConstant(msg.Header.Type)
							if Type.String() == "auditConstant("+strconv.Itoa(int(msg.Header.Type))+")" {
								err = errors.New("Unknown Type: " + string(msg.Header.Type))
							} else {
								m = "type=" + Type.String()[6:] + " msg=" + string(msg.Data[:]) + "\n"
							}
						}
						cb(m, err, args...)
					}
				}
			}
		}
	}()
}

// GetRawAuditEvents receives raw audit messages from kernel parses them to AuditEvent struct.
// It passes them along the callback function and if any error occurs while receiving the message,
// the same will be passed in the callback as well.
// Code that receives the message runs inside a go-routine.
func GetRawAuditMessages(s Netlink, cb RawEventTypeCallback, done *chan bool, args ...interface{}) {
	//rb := make([]byte, syscall.NLMSG_HDRLEN+MAX_AUDIT_MESSAGE_LENGTH)

	for {
		select {
		case <-*done:
			//fmt.Printf("Loop Done %v\n", info)
			return
		default:
			//fmt.Printf("Loop Receive\n")
			b, err := s.ReceiveNoParse(syscall.NLMSG_HDRLEN+MAX_AUDIT_MESSAGE_LENGTH, 0, nil)
			if err == nil {
				for len(b) >= syscall.NLMSG_HDRLEN {
					h := (*syscall.NlMsghdr)(unsafe.Pointer(&b[0]))
					if int(h.Len) < syscall.NLMSG_HDRLEN || int(h.Len) > len(b) {
						break
					}
					b = b[syscall.NLMSG_HDRLEN:]
					dlen := nlmAlignOf(int(h.Len)) - syscall.NLMSG_HDRLEN

					if err != nil {
						break
					}
					if len(b) == int(h.Len) || dlen == int(h.Len) {
						// this should never be possible in correct scenarios
						// but sometimes kernel reponse have length of header == length of data appended
						// which would lead to trimming of data if we subtract NLMSG_HDRLEN
						// therefore following workaround
						//m = NetlinkMessage{Header: *h, Data: dbuf[:int(h.Len)]}
						if h.Type == syscall.NLMSG_ERROR {
							v := int32(nativeEndian().Uint32(b[0:4]))
							if v != 0 {
								cb(h.Type, string(b[:h.Len]), fmt.Errorf("error receiving events %d", v), args...)
							}
						} else {
							cb(h.Type, string(b[:int(h.Len)]), nil, args...)
						}
					} else {
						//m = NetlinkMessage{Header: *h, Data: dbuf[:int(h.Len)-syscall.NLMSG_HDRLEN]}
						if h.Type == syscall.NLMSG_ERROR {
							v := int32(nativeEndian().Uint32(b[0:4]))
							if v != 0 {
								cb(h.Type, string(b[:int(h.Len)-syscall.NLMSG_HDRLEN]), fmt.Errorf("error receiving events %d", v), args...)
							}
						} else {
							cb(h.Type, string(b[:int(h.Len)-syscall.NLMSG_HDRLEN]), nil, args...)
						}
					}
					b = b[dlen:]
				}
				/**
				for _, msg := range msgs {
					if msg.Header.Type == syscall.NLMSG_ERROR {
						v := int32(nativeEndian().Uint32(msg.Data[0:4]))
						if v != 0 {
							cb(msg.Header.Type, string(msg.Data[:]), fmt.Errorf("error receiving events %d", v), args...)
						}
					} else {
						cb(msg.Header.Type, string(msg.Data[:]), nil, args...)
					}
				}
				**/
			}
			//fmt.Printf("Loop Done Receive\n")
		}
	}
}

// GetAuditMessages is a blocking function (runs in forever for loop) that
// receives audit messages from kernel and parses them to AuditEvent.
// It passes them along the callback function and if any error occurs while receiving the message,
// the same will be passed in the callback as well.
// It will return when a signal is received on the done channel.
func GetAuditMessages(s Netlink, cb EventCallback, done *chan bool, args ...interface{}) {
	rb := make([]byte, syscall.NLMSG_HDRLEN+MAX_AUDIT_MESSAGE_LENGTH)

	for {
		select {
		case <-*done:
			return
		default:
			msgs, err := s.Receive(syscall.NLMSG_HDRLEN+MAX_AUDIT_MESSAGE_LENGTH, 0, rb)
			if err == nil {
				for _, msg := range msgs {
					if msg.Header.Type == syscall.NLMSG_ERROR {
						v := int32(nativeEndian().Uint32(msg.Data[0:4]))
						if v != 0 {
							cb(nil, fmt.Errorf("error receiving events %d", v), args...)
						}
					} else {
						nae, err := NewAuditEvent(msg)
						cb(nae, err, args...)
					}
				}
			}
		}
	}

}
