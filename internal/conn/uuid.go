package conn

import (
	"crypto/md5"

	"github.com/Satoru314/go-mcdriver/internal/protocol"
)

// OfflineUUID はオフラインモードのプレイヤーの UUID を作る。
// Java の UUID.nameUUIDFromBytes("OfflinePlayer:"+name) と同じ（MD5 から作る version 3 の UUID）。
func OfflineUUID(name string) protocol.UUID {
	sum := md5.Sum([]byte("OfflinePlayer:" + name))
	sum[6] = sum[6]&0x0f | 0x30 // version 3
	sum[8] = sum[8]&0x3f | 0x80 // variant（IETF）
	return protocol.UUID(sum)
}
