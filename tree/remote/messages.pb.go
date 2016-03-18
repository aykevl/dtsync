// Code generated by protoc-gen-go.
// source: messages.proto
// DO NOT EDIT!

/*
Package remote is a generated protocol buffer package.

It is generated from these files:
	messages.proto

It has these top-level messages:
	FileInfo
	Request
	Response
*/
package remote

import proto "github.com/golang/protobuf/proto"
import fmt "fmt"
import math "math"

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
const _ = proto.ProtoPackageIsVersion1

type Command int32

const (
	Command_NONE    Command = 0
	Command_CLOSE   Command = 1
	Command_DATA    Command = 2
	Command_SCAN    Command = 3
	Command_MKDIR   Command = 10
	Command_REMOVE  Command = 11
	Command_CREATE  Command = 14
	Command_UPDATE  Command = 15
	Command_COPYSRC Command = 16
	Command_GETFILE Command = 20
	Command_SETFILE Command = 21
	// testing commands
	Command_GETCONT Command = 30
	Command_ADDFILE Command = 31
	Command_SETCONT Command = 32
	Command_INFO    Command = 33
)

var Command_name = map[int32]string{
	0:  "NONE",
	1:  "CLOSE",
	2:  "DATA",
	3:  "SCAN",
	10: "MKDIR",
	11: "REMOVE",
	14: "CREATE",
	15: "UPDATE",
	16: "COPYSRC",
	20: "GETFILE",
	21: "SETFILE",
	30: "GETCONT",
	31: "ADDFILE",
	32: "SETCONT",
	33: "INFO",
}
var Command_value = map[string]int32{
	"NONE":    0,
	"CLOSE":   1,
	"DATA":    2,
	"SCAN":    3,
	"MKDIR":   10,
	"REMOVE":  11,
	"CREATE":  14,
	"UPDATE":  15,
	"COPYSRC": 16,
	"GETFILE": 20,
	"SETFILE": 21,
	"GETCONT": 30,
	"ADDFILE": 31,
	"SETCONT": 32,
	"INFO":    33,
}

func (x Command) Enum() *Command {
	p := new(Command)
	*p = x
	return p
}
func (x Command) String() string {
	return proto.EnumName(Command_name, int32(x))
}
func (x *Command) UnmarshalJSON(data []byte) error {
	value, err := proto.UnmarshalJSONEnum(Command_value, data, "Command")
	if err != nil {
		return err
	}
	*x = Command(value)
	return nil
}
func (Command) EnumDescriptor() ([]byte, []int) { return fileDescriptor0, []int{0} }

// See TYPE_* constants in tree/tree.go
type FileType int32

const (
	FileType_UNKNOWN   FileType = 0
	FileType_REGULAR   FileType = 1
	FileType_DIRECTORY FileType = 2
)

var FileType_name = map[int32]string{
	0: "UNKNOWN",
	1: "REGULAR",
	2: "DIRECTORY",
}
var FileType_value = map[string]int32{
	"UNKNOWN":   0,
	"REGULAR":   1,
	"DIRECTORY": 2,
}

func (x FileType) Enum() *FileType {
	p := new(FileType)
	*p = x
	return p
}
func (x FileType) String() string {
	return proto.EnumName(FileType_name, int32(x))
}
func (x *FileType) UnmarshalJSON(data []byte) error {
	value, err := proto.UnmarshalJSONEnum(FileType_value, data, "FileType")
	if err != nil {
		return err
	}
	*x = FileType(value)
	return nil
}
func (FileType) EnumDescriptor() ([]byte, []int) { return fileDescriptor0, []int{1} }

type DataStatus int32

const (
	DataStatus_NORMAL DataStatus = 0
	DataStatus_FINISH DataStatus = 1
	DataStatus_CANCEL DataStatus = 2
)

var DataStatus_name = map[int32]string{
	0: "NORMAL",
	1: "FINISH",
	2: "CANCEL",
}
var DataStatus_value = map[string]int32{
	"NORMAL": 0,
	"FINISH": 1,
	"CANCEL": 2,
}

func (x DataStatus) Enum() *DataStatus {
	p := new(DataStatus)
	*p = x
	return p
}
func (x DataStatus) String() string {
	return proto.EnumName(DataStatus_name, int32(x))
}
func (x *DataStatus) UnmarshalJSON(data []byte) error {
	value, err := proto.UnmarshalJSONEnum(DataStatus_value, data, "DataStatus")
	if err != nil {
		return err
	}
	*x = DataStatus(value)
	return nil
}
func (DataStatus) EnumDescriptor() ([]byte, []int) { return fileDescriptor0, []int{2} }

type FileInfo struct {
	Path             []string  `protobuf:"bytes,1,rep,name=path" json:"path,omitempty"`
	Type             *FileType `protobuf:"varint,2,opt,name=type,enum=remote.FileType" json:"type,omitempty"`
	ModTime          *int64    `protobuf:"zigzag64,3,opt,name=modTime" json:"modTime,omitempty"`
	Size             *int64    `protobuf:"zigzag64,4,opt,name=size" json:"size,omitempty"`
	Hash             []byte    `protobuf:"bytes,5,opt,name=hash" json:"hash,omitempty"`
	XXX_unrecognized []byte    `json:"-"`
}

func (m *FileInfo) Reset()                    { *m = FileInfo{} }
func (m *FileInfo) String() string            { return proto.CompactTextString(m) }
func (*FileInfo) ProtoMessage()               {}
func (*FileInfo) Descriptor() ([]byte, []int) { return fileDescriptor0, []int{0} }

func (m *FileInfo) GetPath() []string {
	if m != nil {
		return m.Path
	}
	return nil
}

func (m *FileInfo) GetType() FileType {
	if m != nil && m.Type != nil {
		return *m.Type
	}
	return FileType_UNKNOWN
}

func (m *FileInfo) GetModTime() int64 {
	if m != nil && m.ModTime != nil {
		return *m.ModTime
	}
	return 0
}

func (m *FileInfo) GetSize() int64 {
	if m != nil && m.Size != nil {
		return *m.Size
	}
	return 0
}

func (m *FileInfo) GetHash() []byte {
	if m != nil {
		return m.Hash
	}
	return nil
}

type Request struct {
	Command          *Command    `protobuf:"varint,1,opt,name=command,enum=remote.Command" json:"command,omitempty"`
	RequestId        *uint64     `protobuf:"varint,2,opt,name=requestId" json:"requestId,omitempty"`
	FileInfo1        *FileInfo   `protobuf:"bytes,3,opt,name=fileInfo1" json:"fileInfo1,omitempty"`
	FileInfo2        *FileInfo   `protobuf:"bytes,4,opt,name=fileInfo2" json:"fileInfo2,omitempty"`
	Name             *string     `protobuf:"bytes,5,opt,name=name" json:"name,omitempty"`
	Data             []byte      `protobuf:"bytes,6,opt,name=data" json:"data,omitempty"`
	Status           *DataStatus `protobuf:"varint,7,opt,name=status,enum=remote.DataStatus" json:"status,omitempty"`
	XXX_unrecognized []byte      `json:"-"`
}

func (m *Request) Reset()                    { *m = Request{} }
func (m *Request) String() string            { return proto.CompactTextString(m) }
func (*Request) ProtoMessage()               {}
func (*Request) Descriptor() ([]byte, []int) { return fileDescriptor0, []int{1} }

func (m *Request) GetCommand() Command {
	if m != nil && m.Command != nil {
		return *m.Command
	}
	return Command_NONE
}

func (m *Request) GetRequestId() uint64 {
	if m != nil && m.RequestId != nil {
		return *m.RequestId
	}
	return 0
}

func (m *Request) GetFileInfo1() *FileInfo {
	if m != nil {
		return m.FileInfo1
	}
	return nil
}

func (m *Request) GetFileInfo2() *FileInfo {
	if m != nil {
		return m.FileInfo2
	}
	return nil
}

func (m *Request) GetName() string {
	if m != nil && m.Name != nil {
		return *m.Name
	}
	return ""
}

func (m *Request) GetData() []byte {
	if m != nil {
		return m.Data
	}
	return nil
}

func (m *Request) GetStatus() DataStatus {
	if m != nil && m.Status != nil {
		return *m.Status
	}
	return DataStatus_NORMAL
}

type Response struct {
	Command          *Command  `protobuf:"varint,1,opt,name=command,enum=remote.Command" json:"command,omitempty"`
	RequestId        *uint64   `protobuf:"varint,2,opt,name=requestId" json:"requestId,omitempty"`
	Error            *string   `protobuf:"bytes,3,opt,name=error" json:"error,omitempty"`
	FileInfo         *FileInfo `protobuf:"bytes,4,opt,name=fileInfo" json:"fileInfo,omitempty"`
	ParentInfo       *FileInfo `protobuf:"bytes,5,opt,name=parentInfo" json:"parentInfo,omitempty"`
	Data             []byte    `protobuf:"bytes,6,opt,name=data" json:"data,omitempty"`
	XXX_unrecognized []byte    `json:"-"`
}

func (m *Response) Reset()                    { *m = Response{} }
func (m *Response) String() string            { return proto.CompactTextString(m) }
func (*Response) ProtoMessage()               {}
func (*Response) Descriptor() ([]byte, []int) { return fileDescriptor0, []int{2} }

func (m *Response) GetCommand() Command {
	if m != nil && m.Command != nil {
		return *m.Command
	}
	return Command_NONE
}

func (m *Response) GetRequestId() uint64 {
	if m != nil && m.RequestId != nil {
		return *m.RequestId
	}
	return 0
}

func (m *Response) GetError() string {
	if m != nil && m.Error != nil {
		return *m.Error
	}
	return ""
}

func (m *Response) GetFileInfo() *FileInfo {
	if m != nil {
		return m.FileInfo
	}
	return nil
}

func (m *Response) GetParentInfo() *FileInfo {
	if m != nil {
		return m.ParentInfo
	}
	return nil
}

func (m *Response) GetData() []byte {
	if m != nil {
		return m.Data
	}
	return nil
}

func init() {
	proto.RegisterType((*FileInfo)(nil), "remote.FileInfo")
	proto.RegisterType((*Request)(nil), "remote.Request")
	proto.RegisterType((*Response)(nil), "remote.Response")
	proto.RegisterEnum("remote.Command", Command_name, Command_value)
	proto.RegisterEnum("remote.FileType", FileType_name, FileType_value)
	proto.RegisterEnum("remote.DataStatus", DataStatus_name, DataStatus_value)
}

var fileDescriptor0 = []byte{
	// 472 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x09, 0x6e, 0x88, 0x02, 0xff, 0x9c, 0x92, 0xdf, 0x6e, 0xd3, 0x30,
	0x14, 0xc6, 0x49, 0x97, 0xfe, 0xc9, 0xe9, 0xd6, 0x1a, 0x0b, 0xa4, 0x5c, 0x8d, 0x52, 0xb8, 0x98,
	0x7a, 0x51, 0xc1, 0x78, 0x82, 0x28, 0x71, 0x47, 0xb4, 0xd4, 0x99, 0x9c, 0x14, 0xb4, 0xcb, 0x88,
	0x7a, 0xb4, 0x12, 0xf9, 0x43, 0xec, 0x5d, 0xc0, 0x03, 0xf1, 0x0e, 0xbc, 0x01, 0x8f, 0x85, 0x8f,
	0xdb, 0x68, 0x20, 0xb1, 0x1b, 0xee, 0xce, 0xe7, 0xf3, 0x1d, 0x9f, 0xdf, 0x97, 0x18, 0x26, 0xa5,
	0x54, 0xaa, 0xf8, 0x2c, 0xd5, 0xb2, 0x69, 0x6b, 0x5d, 0xd3, 0x41, 0x2b, 0xcb, 0x5a, 0xcb, 0xb9,
	0x84, 0xd1, 0x6a, 0xff, 0x45, 0xc6, 0xd5, 0x5d, 0x4d, 0x4f, 0xc1, 0x6d, 0x0a, 0xbd, 0xf3, 0x9d,
	0xd9, 0xc9, 0x85, 0x47, 0xcf, 0xc1, 0xd5, 0xdf, 0x1a, 0xe9, 0xf7, 0x66, 0xce, 0xc5, 0xe4, 0x92,
	0x2c, 0x0f, 0x03, 0x4b, 0x74, 0xe7, 0xe6, 0x9c, 0x4e, 0x61, 0x58, 0xd6, 0xdb, 0x7c, 0x5f, 0x4a,
	0xff, 0xc4, 0x58, 0x28, 0x8e, 0xab, 0xfd, 0x77, 0xe9, 0xbb, 0x9d, 0xda, 0x15, 0x6a, 0xe7, 0xf7,
	0x8d, 0x3a, 0x9d, 0xff, 0x72, 0x60, 0x28, 0xe4, 0xd7, 0x7b, 0xa9, 0x34, 0x9d, 0xc1, 0xf0, 0x53,
	0x5d, 0x96, 0x45, 0xb5, 0x35, 0x9b, 0xf0, 0xee, 0x69, 0x77, 0x77, 0x78, 0x38, 0xa6, 0x4f, 0xc1,
	0x6b, 0x0f, 0xe6, 0x78, 0x6b, 0xf7, 0xbb, 0xf4, 0x15, 0x78, 0x77, 0x47, 0xce, 0xb7, 0x76, 0xdf,
	0xf8, 0x6f, 0x24, 0x1b, 0xe0, 0x0f, 0xd3, 0xa5, 0xc5, 0xf8, 0x97, 0xc9, 0x80, 0x55, 0x85, 0x81,
	0x46, 0x30, 0x0f, 0xd5, 0xb6, 0xd0, 0x85, 0x3f, 0x40, 0x4c, 0x3a, 0x87, 0x81, 0xd2, 0x85, 0xbe,
	0x57, 0xfe, 0xd0, 0x92, 0xd1, 0x6e, 0x3a, 0x32, 0x9e, 0xcc, 0x76, 0xe6, 0x3f, 0x1c, 0x18, 0x09,
	0xa9, 0x9a, 0xba, 0x52, 0xf2, 0xff, 0xb2, 0x9c, 0x41, 0x5f, 0xb6, 0x6d, 0xdd, 0xda, 0x1c, 0x9e,
	0x59, 0x3a, 0xea, 0xa8, 0x1f, 0x85, 0x7e, 0x0d, 0xd0, 0x14, 0xad, 0xac, 0xb4, 0x75, 0xf5, 0x1f,
	0x8f, 0xf6, 0x10, 0x66, 0xf1, 0xd3, 0x7c, 0xf3, 0x8e, 0x62, 0x04, 0x2e, 0x4f, 0x39, 0x23, 0x4f,
	0xa8, 0x07, 0xfd, 0x30, 0x49, 0x33, 0x46, 0x1c, 0x3c, 0x8c, 0x82, 0x3c, 0x20, 0x3d, 0xac, 0xb2,
	0x30, 0xe0, 0xe4, 0x04, 0xdb, 0xeb, 0xeb, 0x28, 0x16, 0x04, 0x28, 0xc0, 0x40, 0xb0, 0x75, 0xfa,
	0x81, 0x91, 0x31, 0xd6, 0xa1, 0x60, 0x41, 0xce, 0xc8, 0x04, 0xeb, 0xcd, 0x4d, 0x84, 0xf5, 0x94,
	0x8e, 0xcd, 0x8a, 0xf4, 0xe6, 0x36, 0x13, 0x21, 0x21, 0x28, 0xae, 0x58, 0xbe, 0x8a, 0x13, 0x46,
	0x9e, 0xa1, 0xc8, 0x8e, 0xe2, 0xf9, 0xb1, 0x13, 0xa6, 0x3c, 0x27, 0xe7, 0x28, 0x82, 0x28, 0xb2,
	0x9d, 0x17, 0x47, 0x9b, 0xed, 0xcc, 0x10, 0x23, 0xe6, 0xab, 0x94, 0xbc, 0x5c, 0xbc, 0x3b, 0x3c,
	0x4b, 0xfb, 0xd0, 0x8c, 0x65, 0xc3, 0xaf, 0x79, 0xfa, 0x91, 0x1b, 0x7c, 0x23, 0x04, 0xbb, 0xda,
	0x24, 0x81, 0x30, 0x01, 0xce, 0xc0, 0x33, 0xa8, 0x2c, 0xcc, 0x53, 0x71, 0x4b, 0x7a, 0x8b, 0x37,
	0x00, 0x0f, 0xff, 0x09, 0x31, 0x79, 0x2a, 0xd6, 0x41, 0x62, 0xa6, 0x4c, 0xbd, 0x8a, 0x79, 0x9c,
	0xbd, 0x37, 0x43, 0x18, 0x25, 0xe0, 0x21, 0x4b, 0x48, 0xef, 0x77, 0x00, 0x00, 0x00, 0xff, 0xff,
	0x75, 0xac, 0x5b, 0x29, 0x16, 0x03, 0x00, 0x00,
}
