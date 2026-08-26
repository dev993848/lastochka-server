package main

import (
	"encoding/base64"
	"fmt"

	_ "github.com/tinode/chat/server/auth/token"
	_ "github.com/tinode/chat/server/auth/basic"
	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/store"
	"github.com/tinode/chat/server/store/types"
)

func main() {
	h := store.Store.GetLogicalAuthHandler("token")
	if h == nil || !h.IsInitialized() {
		fmt.Println("token handler not initialized")
		return
	}
	rec := &auth.Rec{
		Uid:       types.Uid(1),
		AuthLevel: auth.LevelAuth,
		Features:  auth.FeatureNoLogin,
	}
	tok, expires, err := h.GenSecret(rec)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("token-bytes-len:", len(tok))
	fmt.Println("token-b64:", base64.StdEncoding.EncodeToString(tok))
	fmt.Println("expires:", expires.UTC().Format("2006-01-02T15:04:05Z07:00"))
}
