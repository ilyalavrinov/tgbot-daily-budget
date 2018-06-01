package budget

import "log"
import "../botcfg"

var redisServer string
var redisDB int

func Init(cfg botcfg.Config) {
    redisServer = cfg.Redis.Server
    redisDB = cfg.Redis.DB
}

func CreateStorageConnection() Storage {
    return NewRedisStorage(redisServer, redisDB)
}

func GetWalletForOwner(owner OwnerId, createIfAbsent bool, storageconn Storage) (*Wallet, error) {
    log.Printf("Acquiring wallet for owner %d", owner)
    wallet, err := storageconn.GetWalletForOwner(owner, createIfAbsent)
    if err != nil {
        log.Printf("Could not get wallet for owner %d due to error: %s", owner, err)
        return nil, err
    }

    return wallet, nil
}
