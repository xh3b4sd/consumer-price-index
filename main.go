package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/xh3b4sd/budget/v3"
	"github.com/xh3b4sd/budget/v3/pkg/breaker"
	"github.com/xh3b4sd/framer"
)

const (
	// A registration key can be used for increased API limits, but this key has
	// to be renewed every year. During normal collection the public API limit
	// should be sufficient.
	//
	//     apifmt = "https://api.bls.gov/publicAPI/v2/timeseries/data/CUUR0000SA0?registrationkey=%s&startyear=%s&endyear=%s"
	//
	apifmt = "https://api.bls.gov/publicAPI/v2/timeseries/data/CUUR0000SA0?startyear=%s&endyear=%s"
	dayzer = "2020-12-01T00:00:00Z"
	reqlim = 10
	rewfil = "inflation.csv"
)

type csvrow struct {
	Dat       time.Time
	Inflation float64
	Updated   int
}

type resstr struct {
	Results resstrres `json:"Results"`
}

type resstrres struct {
	Series []resstrser `json:"series"`
}

type resstrser struct {
	Data []resstrdat `json:"data"`
}

type resstrdat struct {
	Period string `json:"period"`
	Value  string `json:"value"`
	Year   string `json:"year"`
}

func main() {
	var err error

	var rea *os.File
	{
		rea, err = os.Open(rewfil)
		if err != nil {
			log.Fatal(err)
		}
	}

	var row [][]string
	{
		row, err = csv.NewReader(rea).ReadAll()
		if err != nil {
			log.Fatal(err)
		}
	}

	{
		rea.Close()
	}

	cur := map[time.Time]float64{}
	for _, x := range row[1:] {
		if x[2] == "0" {
			cur[mustim(x[0])] = -musf64(x[1])
		}

		if x[2] == "1" {
			cur[mustim(x[0])] = musf64(x[1])
		}
	}

	var sta time.Time
	{
		sta = mustim(dayzer)
	}

	var end time.Time
	{
		end = time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.UTC)
	}

	var bud budget.Interface
	{
		bud = breaker.Default()
	}

	var fra *framer.Framer
	{
		fra = framer.New(framer.Config{
			Sta: sta,
			End: end,
			Len: 24 * time.Hour,
		})
	}

	var cou int
	des := map[time.Time]float64{}
	for _, x := range fra.List() {
		f64, exi := cur[x.Sta]
		if exi && f64 > 0 {
			{
				// log.Printf("setting cached inflation for %s\n", x.Sta)
			}

			{
				des[x.Sta] = f64
			}
		} else if cou < reqlim {
			if !exi {
				cou++
			}

			{
				log.Printf("filling remote inflation for %s\n", x.Sta)
			}

			var act func() error
			{
				act = func() error {
					var f64 float64
					{
						f64 = musapi(x.Sta)
					}

					var pre float64
					{
						pre = des[x.Sta.Add(-24*time.Hour)]
					}

					if f64 == -1 && pre > 0 {
						f64 = -pre
					}

					if f64 == -1 && pre < 0 {
						f64 = pre
					}

					{
						des[x.Sta] = f64
					}

					return nil
				}
			}

			{
				err = bud.Execute(act)
				if budget.IsCancel(err) {
					break
				} else if budget.IsPassed(err) {
					break
				} else if err != nil {
					log.Fatal(err)
				}
			}

			{
				time.Sleep(200 * time.Millisecond)
			}
		}
	}

	var lis []csvrow
	for k, v := range des {
		var inf float64
		{
			inf = math.Abs(v)
		}

		var upd int
		if v > 0 {
			upd = 1
		}

		{
			lis = append(lis, csvrow{Dat: k, Inflation: inf, Updated: upd})
		}
	}

	{
		sort.SliceStable(lis, func(i, j int) bool { return lis[i].Dat.Before(lis[j].Dat) })
	}

	var res [][]string
	{
		res = append(res, []string{"date", "inflation", "updated"})
	}

	for _, x := range lis {
		res = append(res, []string{x.Dat.Format(time.RFC3339), fmt.Sprintf("%.5f", x.Inflation), fmt.Sprintf("%d", x.Updated)})
	}

	var wri *os.File
	{
		wri, err = os.OpenFile(rewfil, os.O_RDWR|os.O_CREATE|os.O_TRUNC, os.ModePerm)
		if err != nil {
			log.Fatal(err)
		}
	}

	{
		defer wri.Close()
	}

	{
		err = csv.NewWriter(wri).WriteAll(res)
		if err != nil {
			log.Fatal(err)
		}
	}
}

func musapi(des time.Time) float64 {
	var err error

	if time.Now().Month() == des.Month() {
		return -1
	}

	// var key string
	// {
	// 	key = muskey()
	// }

	var sta string
	var end string
	{
		sta = des.AddDate(-2, 0, 0).Format("2006")
		end = des.Format("2006")
	}

	var cli *http.Client
	{
		cli = &http.Client{Timeout: 10 * time.Second}
	}

	var res *http.Response
	{
		u := fmt.Sprintf(apifmt /*key,*/, sta, end)

		res, err = cli.Get(u)
		if err != nil {
			log.Fatal(err)
		}
	}

	{
		defer res.Body.Close()
	}

	var byt []byte
	{
		byt, err = io.ReadAll(res.Body)
		if err != nil {
			log.Fatal(err)
		}
	}

	var dat resstr
	{
		err = json.Unmarshal(byt, &dat)
		if err != nil {
			log.Fatal(err)
		}
	}

	var pry string
	var cyr string
	{
		pry = des.AddDate(-1, 0, 0).Format("2006")
		cyr = des.Format("2006")
	}

	var mon string
	{
		mon = fmt.Sprintf("M%s", des.Format("01"))
	}

	if len(dat.Results.Series) != 1 {
		panic(string(byt))
	}

	var pre float64
	var cur float64
	for _, x := range dat.Results.Series[0].Data {
		if x.Year == pry && x.Period == mon {
			pre = musf64(x.Value)
		}
		if x.Year == cyr && x.Period == mon {
			cur = musf64(x.Value)
		}
	}

	if pre == 0 && cur == 0 {
		return -1
	}

	return (cur / pre) - 1
}

// func muskey() string {
// 	key := os.Getenv("BLS_API_KEY")
// 	if key == "" {
// 		panic("${BLS_API_KEY} must not be empty")
// 	}

// 	return key
// }

func musf64(str string) float64 {
	f64, err := strconv.ParseFloat(str, 64)
	if err != nil {
		log.Fatal(err)
	}

	return f64
}

func mustim(str string) time.Time {
	tim, err := time.Parse(time.RFC3339, str)
	if err != nil {
		log.Fatal(err)
	}

	return tim
}
