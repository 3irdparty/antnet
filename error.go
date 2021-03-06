package antnet

import (
	"errors"
)

var idErrMap = map[uint16]error{}
var errIdMap = map[error]uint16{}

func NewError(str string, id uint16) error {
	e := errors.New(str)
	SetErrorId(e, id)
	return e
}

func SetErrorId(err error, id uint16) {
	idErrMap[id] = err
	errIdMap[err] = id
}

var (
	ErrDBErr         = NewError("数据库错误", 1)
	ErrProtoPack     = NewError("协议解析错误", 2)
	ErrProtoUnPack   = NewError("协议打包错误", 3)
	ErrMsgPackPack   = NewError("msgpack打包错误", 4)
	ErrMsgPackUnPack = NewError("msgpack解析错误", 5)
	ErrPBPack        = NewError("pb打包错误", 6)
	ErrPBUnPack      = NewError("pb解析错误", 7)
	ErrJsonPack      = NewError("json打包错误", 8)
	ErrJsonUnPack    = NewError("json解析错误", 9)
	ErrCmdUnPack     = NewError("cmd解析错误", 10)
	ErrFileRead      = NewError("文件读取错误", 100)
	ErrErrIdNotFound = NewError("错误没有对应的错误码", 255)
)

var MinUserError = 256

func GetErrById(id uint16) error {
	if e, ok := idErrMap[id]; ok {
		return e
	}
	return nil
}

func GetErrId(err error) uint16 {
	if id, ok := errIdMap[err]; ok {
		return id
	}
	return errIdMap[ErrErrIdNotFound]
}
