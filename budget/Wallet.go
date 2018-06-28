package budget

import "log"
import "fmt"
import "time"
import "math"
import "errors"

type WalletId string

type Wallet struct {
    ID WalletId
    MonthStart int
    storage Storage
}

func NewWalletFromStorage(id string, monthStart int, storageconn Storage) *Wallet {
    wallet := &Wallet { ID: WalletId(id),
                        MonthStart: monthStart,
                        storage: storageconn}
    return wallet
}

func (w *Wallet) AddTransaction(t ActualTransaction) error {
    return w.storage.AddActualTransaction(w.ID, t)
}

func checkRegularTransactionLabelExist(transactions []*RegularTransaction, label string) bool {
    for _, t := range transactions {
        if t.Label == label {
            return true
        }
    }
    return false
}

func (w *Wallet) AddRegularTransaction(t RegularTransaction) error {
    date := t.Date
    if date < 1 || date > 28 {
        return errors.New("Only dates between 1 and 28 are allowed for regular income/outcome setting")
    }

    transactions, err := w.storage.GetRegularTransactions(w.ID)
    if err != nil {
        log.Printf("Could not add regular transactions - unable to get a list of all current regulars for wallet '%s'; error: %s", w.ID, err)
        return err
    }

    exists := checkRegularTransactionLabelExist(transactions, t.Label)
    if exists {
        log.Printf("Label '%s' already exists for wallet '%s', cannot add regular transaction", t.Label, w.ID)
        return errors.New(fmt.Sprintf("Label '%s' already exists", t.Label))
    }

    return w.storage.AddRegularTransaction(w.ID, t)
}

func (w *Wallet) GetPlannedMonthlyIncome() (int, error) {
    log.Printf("Calculating planned monthly income for wallet '%s'", w.ID)
    transactions, err := w.storage.GetRegularTransactions(w.ID)
    if err != nil {
        log.Printf("Could not get monthly transactions for wallet '%s', error: %s", w.ID, err)
        return 0, err
    }

    totalIncome := 0
    for _, change := range transactions {
        totalIncome += change.Value
    }

    log.Printf("Total income for wallet '%s' is %d", w.ID, totalIncome)

    return totalIncome, nil
}

func checkRegularTransactionExactMatchExist(transactions []*RegularTransaction, t_checked RegularTransaction) bool {
    for _, t := range transactions {
        if *t == t_checked {
            return true
        }
    }
    return false
}

func (w *Wallet) RemoveRegularTransaction(t RegularTransaction) error {
    transactions, err := w.storage.GetRegularTransactions(w.ID)
    if err != nil {
        log.Printf("Could not remove regular transactions - unable to get a list of all current regulars for wallet '%s'; error: %s", w.ID, err)
        return err
    }

    exists := checkRegularTransactionExactMatchExist(transactions, t)
    if !exists {
        log.Printf("There are no exactly matched regular transaction for wallet '%s', cannot remove regular transaction", w.ID)
        return errors.New(fmt.Sprintf("Label '%s' already exists", t.Label))
    }

    return w.storage.RemoveRegularTransaction(w.ID, t)
}

func calcCurMonthBorders(walletMonthStartDay int, now time.Time) (time.Time, time.Time) {
    if walletMonthStartDay < 1 || walletMonthStartDay > 28 {
        panic("Date must be between 1 and 28")
    }

    monthStart := time.Date(now.Year(), now.Month(), walletMonthStartDay, 0, 0, 0, 0, time.Local) // TODO: check whether UTC or Local is needed
    if now.Day() < walletMonthStartDay {
        // we've switched the month already, monthStart should be at the previous month
        monthStart = monthStart.AddDate(0, -1, 0)
    }
    monthEnd := monthStart.AddDate(0, 1, 0)
    log.Printf("Month borders are from %s to %s", monthStart, monthEnd)
    return monthStart, monthEnd
}

func (w *Wallet) getRegularTransactions() ([]*RegularTransaction, error) {
    transactions, err := w.storage.GetRegularTransactions(w.ID)
    if err != nil {
        return nil, err
    }
    return transactions, nil
}

func (w *Wallet) getActualTransactionsForCurrentMonthTillDate(t time.Time) ([]*ActualTransaction, error) {
    // TODO: cache results of actual transactions so we don't need to call it again
    t1, _ := calcCurMonthBorders(w.MonthStart, t)
    transactions, err := w.storage.GetActualTransactions(w.ID, t1, t)
    if err != nil {
        return nil, err
    }
    return transactions, nil
}

func accumulateMatchedTransactions(regular_txs []*RegularTransaction, actual_txs []*ActualTransaction) map[string]int {
    // finds which actual transactions correspond to the regulars
    log.Printf("Accumulating matched transactions for a list of regulars[size %d and actuals[size %d]]", len(regular_txs), len(actual_txs))

    regularTransactionsLabeled := make(map[string]bool, len(regular_txs))
    for _, regularElem := range regular_txs {
        if regularElem.Label == "" {
            panic("Label for regular transaction is empty")
        }
        regularTransactionsLabeled[regularElem.Label] = true
    }

    matched := make(map[string]int, len(regular_txs))
    for _, actual := range actual_txs {
        label := actual.Label
        if label == "" {
            continue
        }
        if _, found := regularTransactionsLabeled[label]; !found {
            continue
        }
        // here we have a match - actual transaction has same label as a regular
        matched[label] += actual.Value
    }
    return matched
}

func (w *Wallet) calcMonthlyIncomeTillDate(regular_txs []*RegularTransaction, matched_actual_txs map[string]int, t time.Time) int {
    // calculation of supposedly received income till current date.
    // If there are actual transaction which match planned via labels, final result differs depending on income/expense and its value
    totalMonthlyIncome := 0
    for _, tx := range regular_txs {
        if matched_amount, found := matched_actual_txs[tx.Label]; found {
            if (tx.Value > 0 && matched_amount < 0) || (tx.Value < 0 && matched_amount > 0) {
                log.Printf("Mismatched signs of regular and actual values - regular: %d; actual: %d", tx.Value, matched_amount)
                panic("Mismatched signs for regular and its matched actual counterpart")
            }
            if tx.Value > 0 {
                // no special rule. Let's use the value we've found in matched tx as general recommendation for income is '1 planned -> 1 actual'
                log.Printf("Monthly income calc: for label #%s adding %d: general income case", tx.Label, matched_amount)
                totalMonthlyIncome += matched_amount
            } else { // tx.Value < 0 as == 0 is impossible
                // the rule here: if we've reached the planned value, we use it. Otherwise - using planned still
                // this rule is needed when there are several transaction fulfilling same planned (e.g. #bills are actually split into several payments - for house, phone, etc.)
                if math.Abs(float64(tx.Value)) > math.Abs(float64(matched_amount)) {
                    log.Printf("Monthly income calc: for label #%s adding %d: expense case with planned greater than actual", tx.Label, tx.Value)
                    totalMonthlyIncome += tx.Value
                } else {
                    log.Printf("Monthly income calc: for label #%s adding %d: expense case with actual greater than planned", tx.Label, matched_amount)
                    totalMonthlyIncome += matched_amount
                }
            }
        } else { // we have no matched transactions
            log.Printf("Monthly income calc: for label #%s adding %d: no matched actual", tx.Label, tx.Value)
            totalMonthlyIncome += tx.Value
        }
    }
    log.Printf("Montly income calc: total income equals to %d", totalMonthlyIncome)
    var result float32 = 0
    // calculating result based on hoe many days have passed considering whether we've reached the end of prev month
    // TODO: use calcCurMonthBorders() ?
    curDay := t.Day()
    if curDay >= w.MonthStart {
        daysInCurMonth := daysInMonth[t.Month()]
        result = float32(totalMonthlyIncome) / float32(daysInCurMonth) * float32((curDay - w.MonthStart + 1)) // +1 as we assume that daily portion is granted at the beginning of the day
    } else {
        // tricky code to calc how many days have passed if we've reached the end of the previous month
        prevMonth := time.December
        if t.Month() != time.January {
            prevMonth = t.Month() - 1
        }
        result = float32(totalMonthlyIncome) / float32(31 - (w.MonthStart - curDay) - (31 - daysInMonth[prevMonth]))
    }

    log.Printf("Montly income calc: till date %s it equals to %f", t, result)
    return int(result)
}

func (w *Wallet) calcUnmatchedTransactionsSum(actual_txs []*ActualTransaction, matched_actual_txs map[string]int) int {
    sum := 0
    for _, tx := range actual_txs {
        if _, found := matched_actual_txs[tx.Label]; found {
            // this transaction has already been used for monthly income calculation, skipping it
            continue
        }
        sum += tx.Value
    }
    log.Printf("Unmatched transactions for wallet '%s' sum to %d", w.ID, sum)
    return sum
}

func (w *Wallet) GetBalance(t time.Time) (int, error) {
    log.Printf("Starting to calculate available amount for wallet '%s' for time %s", w.ID, t)

    regular_txs, err := w.getRegularTransactions()
    if err != nil {
        log.Printf("Unable to get regular transactions for wallet '%s'", w.ID)
        return 0, err
    }
    log.Printf("There are %d regular transactions for wallet '%s'", len(regular_txs), w.ID)

    actual_txs, err := w.getActualTransactionsForCurrentMonthTillDate(t)
    if err != nil {
        log.Printf("Unable to get list of actual transactions related to current month for wallet '%s' till date %s", w.ID, t)
        return 0, err
    }
    log.Printf("There are %d regular transactions for wallet '%s'", len(actual_txs), w.ID)

    matched_actual_txs := accumulateMatchedTransactions(regular_txs, actual_txs)
    curAvailIncome := w.calcMonthlyIncomeTillDate(regular_txs, matched_actual_txs, t)
    unmatchedTrxSum := w.calcUnmatchedTransactionsSum(actual_txs, matched_actual_txs)
    availMoney := curAvailIncome + unmatchedTrxSum
    log.Printf("Currently available money for wallet '%s': %d (matched with regular: %d; unmatched: %d)", w.ID, availMoney, curAvailIncome, unmatchedTrxSum)
    return availMoney, nil
}

func (w *Wallet) SetMonthStart(date int) error {
    if date < 1 || date > 28 {
        panic("Date is out of range")
    }
    oldDate := w.MonthStart
    w.MonthStart = date
    err := w.storage.SetWalletInfo(w.ID, w.MonthStart)
    if err != nil {
        log.Printf("Could not update wallet '%s' date from %d to %d. Reverting to oiginal value", w.ID, oldDate, date)
        w.MonthStart = oldDate
    }
    return err
}
