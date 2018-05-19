package budget

import "log"
import "fmt"
import "strconv"
import "errors"
import "time"
import "math"
import "github.com/go-redis/redis"
import "github.com/satori/go.uuid"

var daysInMonth = map[time.Month]int {  time.January: 31,
                                        time.February: 28, // TODO: handle leap year
                                        time.March: 31,
                                        time.April: 30,
                                        time.May: 31,
                                        time.June: 30,
                                        time.July: 31,
                                        time.August: 31,
                                        time.September: 30,
                                        time.October: 31,
                                        time.November: 30,
                                        time.December: 31 }


type RedisStorage struct {
    client *redis.Client
}

func NewRedisStorage(server string) Storage {
    s := &RedisStorage{}
    s.client = redis.NewClient(&redis.Options{
        Addr: server})
    return s
}

func (s *RedisStorage) set(key, value string) error {
    log.Printf("Setting key: %s value: %s", key, value)

    status := s.client.Set(key, value, 0)
    err := status.Err()
    if err != nil {
        log.Printf("Unable to set value %s to key %s; error: %s", value, key, err)
        return err
    }

    return nil
}

func (s *RedisStorage) AddAmountChange(w Wallet, val AmountChange) error {
    operation := "out"
    if val.Value >= 0 {
        operation = "in"
    }
    key := fmt.Sprintf("wallet:%s:%s:%d", w.ID, operation, val.Time.Unix())
    value := strconv.Itoa(val.Value)

    return s.set(key, value)
}

func (s *RedisStorage) AddRegularChange(w Wallet, change MonthlyChange) error {
    date := change.Date
    if date < 1 || date > 28 {
        return errors.New("Only dates between 1 and 28 are allowed for regular income/outcome setting")
    }

    operation := "out"
    if change.Value >= 0 {
        operation = "in"
    }
    key := fmt.Sprintf("wallet:%s:monthly:%s:%d", w.ID, operation, date)

    log.Printf("Setting regular monthly income/outcome with value %d to key %s", change.Value, key)
    return s.client.LPush(key, change.Value).Err()
}

func (s *RedisStorage) GetMonthlyIncome(w Wallet) (int, error) {
    log.Printf("Getting monthly income")
    income := make(map[string]int, 10)
    scanMatch := fmt.Sprintf("wallet:%s:monthly:*", w.ID)
    for {
        var cursor uint64 = 0
        keys, cursor, err := s.client.Scan(cursor, scanMatch, 10).Result()
        if err != nil {
            log.Printf("Error happened during scanning with match: %s; error: %s", scanMatch, err)
            return 0, err
        }

        for _, k := range keys {
            _, found := income[k]
            if found {
                log.Print("Key %s has already been used for monthly income calclation, skipping it", k)
                continue
            }

            log.Print("Getting income values for key %s", k)
            values, err := s.client.LRange(k, math.MinInt64, math.MaxInt64).Result()
            if err != nil {
                log.Printf("Cannot get list for key %s; error: %s", k, err)
                return 0, err
            }

            for _, v := range values {
                val, err := strconv.Atoi(v)
                if err != nil {
                    log.Printf("Could not convert value %s to integer due to error: %s", v, err)
                    return 0, err
                }
                income[k] += val
            }

            log.Printf("Total income for key %s is %d", k, income[k])
        }

        if cursor == 0 {
            log.Printf("Scanning finished")
            break
        }
    }

    totalIncome := 0
    for _, v := range income {
        totalIncome += v
    }
    log.Printf("Total income for wallet %s is %d", w.ID, totalIncome)

    return totalIncome, nil
}

func (s *RedisStorage) getMonthStart(w Wallet) (int, error) {
    log.Printf("Looking for month start for wallet %s", w.ID)
    key := fmt.Sprintf("wallet:%s")
    res := s.client.HGet(key, "monthStart")

    if res == nil {
        log.Printf("Month start value for wallet key %s is not set, using default", key)
        return 1, nil
    }

    if res.Err() != nil {
        log.Printf("Could not get month start for wallet with key %s, error: %s", key, res.Err())
        return 0, res.Err()
    }

    val, err := res.Int64()
    if err != nil {
        log.Printf("Could not convert value %s of month start for wallet with key %s, error: %s", res.Val(), key, err)
        return 0, err
    }

    if val < 1 || val > 28 {
        log.Printf("Month start for wallet key %s is out of expected ranges", key)
        return 0, errors.New("Month start value out of range")
    }

    return int(val), nil
}

func (s *RedisStorage) GetMonthlyIncomeTillDate(w Wallet, t time.Time) (int, error) {
    log.Printf("Calculating monthly income for wallet %s till time %s", w.ID, t)

    monthly, err := s.GetMonthlyIncome(w)
    if err != nil {
        log.Printf("Could not calculate monthly income for wallet %s with error: %s", w.ID, err)
        return 0, err
    }

    if monthly <= 0 {
        log.Printf("Monthly income is %d (not positive), bot cannot work with such values", monthly)
        return 0, errors.New("Negative monthly income")
    }

    log.Printf("Got monthly income for wallet %s equal to %d", w.ID, monthly)

    monthStart, err := s.getMonthStart(w)
    if err != nil {
        log.Printf("Month start for wallet %s could not be retrieved due to error: %s", w.ID, err)
        return 0, err
    }

    log.Printf("Month start for wallet %s is %d", w.ID, monthStart)

    var result float32 = 0
    // calculating result based on hoe many days have passed considering whether we've reached the end of prev month
    curDay := t.Day()
    if curDay >= monthStart {
        daysInCurMonth := daysInMonth[t.Month()]
        result = float32(monthly) / float32(daysInCurMonth * (curDay - monthStart + 1)) // +1 as we assume that daily portion is granted at the beginning of the day
    } else {
        // tricky code to calc how many days have passed if we've reached the end of the previous month
        prevMonth := time.December
        if t.Month() != time.January {
            prevMonth = t.Month() - 1
        }
        result = float32(monthly) / float32(31 - (monthStart - curDay) - (31 - daysInMonth[prevMonth]))
    }

    log.Printf("Calculated montly income till date %s: it equals to %d", t, result)
    return int(result), nil
}

func (s *RedisStorage) GetWalletForUser(userId int) (*Wallet, error) {
    key := fmt.Sprintf("user:%d", userId)
    log.Printf("Getting wallet for user via key %s", key)
    result := s.client.HGetAll(key)
    if result == nil {
        log.Printf("Could not get user info for user with key %s", key)
        // TODO: new user info?
        return nil, errors.New("No user info")
    }

    log.Printf("Got info about user key %s. Info: %v", key, result.Val())
    // TODO: add teams
    walletIdStr, found := result.Val()["wallet"]
    if !found {
        log.Printf("No wallet found for user key %s", key)
        return nil, errors.New("No wallet for user")
        //TODO: request new wallet?
    }

    walletId, err := uuid.FromString(walletIdStr)
    if err != nil {
        log.Printf("Could not convert wallet ID %s to uuid, error: %s", walletIdStr, err)
        return nil, err
    }
    return &Wallet{ID: walletId}, nil
}

func (s *RedisStorage) attachWalletToUser(userKey string, walletId string) error {
    res := s.client.HSet(userKey, "wallet", walletId)

    if res != nil && res.Val() == false {
        log.Printf("Could not attach user '%s' and wallet '%s'", userKey, walletId)
        return errors.New("Could not attach wallet to user")
    }

    log.Printf("Attached user with key '%s' and wallet '%s'", userKey, walletId)
    return nil
}

func (s *RedisStorage) CreateUser(userId int) error {
    log.Printf("Starting creation of user %d", userId)

    key := fmt.Sprintf("user:%d", userId)
    user := s.client.HGetAll(key)
    if user != nil && len(user.Val()) > 0 {
        log.Printf("User %d has been already created", userId)
        return errors.New("User exists")
    }

    walletId, err := s.createWallet()
    if err != nil {
        log.Printf("Could not create wallet for user %d with error: %s", userId, err)
        return err
    }
    log.Printf("Wallet %s has been created for user %d", walletId, userId)

    s.attachWalletToUser(key, walletId)

    return nil
}

func (s *RedisStorage) createWallet() (string, error) {
    final_id := ""
    for final_id == "" {
        id, err := uuid.NewV4()
        if err != nil {
            log.Printf("Could get new wallet UUID due to error: %s", err)
            return "", err
        }

        key := fmt.Sprintf("wallet:%s", id.String())
        log.Printf("Checking if wallet with key %s exists", key)
        result := s.client.HGetAll(key)
        if result != nil && len(result.Val()) > 0 {
            log.Printf("Wallet with key %s exists, trying another one", key)
            continue
        }

        log.Printf("Wallet with key %s doesn't exist, using it", key)
        s.client.HSet(key, "created", time.Now().Unix())
        final_id = id.String()
    }

    return final_id, nil
}
