// Command slabctl is a tiny interactive front end for the stores.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"slab/admin"
	"slab/lru"
	"slab/ttl"
)

func main() {
	cache := lru.New(128)
	store := ttl.NewCache(10 * time.Minute)
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		f := strings.Fields(in.Text())
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "put":
			cache.Put(f[1], []byte(strings.Join(f[2:], " ")))
		case "evict":
			cache.Remove(f[1])
			fmt.Println("ok")
		case "token":
			store.Set(f[1], f[2])
		case "revoke":
			store.Evict(f[1])
		case "purge":
			fmt.Println(admin.PurgeKeys(cache, f[1:]), "purged")
		case "flush":
			fmt.Println(admin.Flush(cache), "flushed")
		}
	}
}
