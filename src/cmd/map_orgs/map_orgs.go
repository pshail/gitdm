package main

import (
	"database/sql"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	json "github.com/json-iterator/go"
	"gopkg.in/yaml.v2"
)

var (
	// MODE
	// gSortKeys = false
	gSortKeys = true
)

// gitHubUsers - list of GitHub user data from cncf/devstats.
type gitHubUsers []gitHubUser

// MODE - use named fields (name type gitHubUserStr -> gitHubUser)
// gitHubUser - single GitHug user entry from cncf/devstats `github_users.json` JSON.
type gitHubUser struct {
	Login       string  `json:"login"`
	Email       string  `json:"email"`
	Affiliation string  `json:"affiliation"`
	Source      *string `json:"source,omitempty"`
	Name        *string `json:"name,omitempty"`
	Commits     *int    `json:"commits,omitempty"`
	Location    *string `json:"location,omitempty"`
	CountryID   *string `json:"country_id,omitempty"`
}

// MODE - use unlimited fields (name type gitHubUserMap -> gitHubUserStr)
type gitHubUserMap map[string]interface{}

// allAcquisitions contain all company acquisitions data
// Acquisition contains acquired company name regular expression and new company name for it.
type allAcquisitions struct {
	Acquisitions [][2]string `yaml:"acquisitions"`
}

// allMappings contain all organization name mappings
type allMappings struct {
	Mappings [][2]string `yaml:"mappings"`
}

func fatalOnError(err error) {
	if err != nil {
		tm := time.Now()
		fmt.Printf("Error(time=%+v):\nError: '%s'\nStacktrace:\n%s\n", tm, err.Error(), string(debug.Stack()))
		fmt.Fprintf(os.Stderr, "Error(time=%+v):\nError: '%s'\nStacktrace:\n", tm, err.Error())
		panic("stacktrace")
	}
}

func fatalf(f string, a ...interface{}) {
	fatalOnError(fmt.Errorf(f, a...))
}

// getConnectString - get MariaDB SH (Sorting Hat) database DSN
// Either provide full DSN via SH_DSN='shuser:shpassword@tcp(shhost:shport)/shdb?charset=utf8'
// Or use some SH_ variables, only SH_PASS is required
// Defaults are: "shuser:required_pwd@tcp(localhost:3306)/shdb?charset=utf8"
// SH_DSN has higher priority; if set no SH_ varaibles are used
func getConnectString() string {
	//dsn := "shuser:"+os.Getenv("PASS")+"@/shdb?charset=utf8")
	dsn := os.Getenv("SH_DSN")
	if dsn == "" {
		pass := os.Getenv("SH_PASS")
		if pass == "" {
			fatalf("please specify database password via SH_PASS=...")
		}
		user := os.Getenv("SH_USER")
		if user == "" {
			user = "shuser"
		}
		proto := os.Getenv("SH_PROTO")
		if proto == "" {
			proto = "tcp"
		}
		host := os.Getenv("SH_HOST")
		if host == "" {
			host = "localhost"
		}
		port := os.Getenv("SH_PORT")
		if port == "" {
			port = "3306"
		}
		db := os.Getenv("SH_DB")
		if db == "" {
			db = "shdb"
		}
		params := os.Getenv("SH_PARAMS")
		if params == "" {
			params = "?charset=utf8"
		}
		if params == "-" {
			params = ""
		}
		dsn = fmt.Sprintf(
			"%s:%s@%s(%s:%s)/%s%s",
			user,
			pass,
			proto,
			host,
			port,
			db,
			params,
		)
	}
	return dsn
}

// getAffiliationsJSONBody - get affiliations JSON contents
// First try to get JSON from SH_LOCAL_JSON_PATH which defaults to "github_users.json"
// Fallback to SH_REMOTE_JSON_PATH which defaults to "https://github.com/cncf/devstats/raw/master/github_users.json"
func getAffiliationsJSONBody() []byte {
	jsonLocalPath := os.Getenv("SH_LOCAL_JSON_PATH")
	if jsonLocalPath == "" {
		jsonLocalPath = "github_users.json"
	}
	data, err := ioutil.ReadFile(jsonLocalPath)
	if err != nil {
		switch err := err.(type) {
		case *os.PathError:
			jsonRemotePath := os.Getenv("SH_REMOTE_JSON_PATH")
			if jsonRemotePath == "" {
				jsonRemotePath = "https://github.com/cncf/devstats/raw/master/github_users.json"
			}
			response, err2 := http.Get(jsonRemotePath)
			fatalOnError(err2)
			defer func() { _ = response.Body.Close() }()
			data, err2 = ioutil.ReadAll(response.Body)
			fatalOnError(err2)
			fmt.Printf("Read %d bytes remote JSON data from %s\n", len(data), jsonRemotePath)
			return data
		default:
			fatalOnError(err)
		}
	}
	fmt.Printf("Read %d bytes local JSON data from %s\n", len(data), jsonLocalPath)
	return data
}

// getAcquisitionsYAMLBody - get company acquisitions and name mappings YAML body
// First try to get YAML from SH_LOCAL_YAML_PATH which defaults to "companies.yaml"
// Fallback to SH_REMOTE_YAML_PATH which defaults to "https://github.com/cncf/devstats/raw/master/companies.yaml"
func getAcquisitionsYAMLBody() []byte {
	yamlLocalPath := os.Getenv("SH_LOCAL_YAML_PATH")
	if yamlLocalPath == "" {
		yamlLocalPath = "companies.yaml"
	}
	data, err := ioutil.ReadFile(yamlLocalPath)
	if err != nil {
		switch err := err.(type) {
		case *os.PathError:
			yamlRemotePath := os.Getenv("SH_REMOTE_YAML_PATH")
			if yamlRemotePath == "" {
				yamlRemotePath = "https://github.com/cncf/devstats/raw/master/companies.yaml"
			}
			response, err2 := http.Get(yamlRemotePath)
			fatalOnError(err2)
			defer func() { _ = response.Body.Close() }()
			data, err2 = ioutil.ReadAll(response.Body)
			fatalOnError(err2)
			fmt.Printf("Read %d bytes remote YAML data from %s\n", len(data), yamlRemotePath)
			return data
		default:
			fatalOnError(err)
		}
	}
	fmt.Printf("Read %d bytes local YAML data from %s\n", len(data), yamlLocalPath)
	return data
}

// getMapOrgNamesYAMLBody - get map organization names YAML body
func getMapOrgNamesYAMLBody() []byte {
	yamlRemotePath := "https://github.com/LF-Engineering/dev-analytics-affiliation/raw/prod/map_org_names.yaml"
	response, err := http.Get(yamlRemotePath)
	fatalOnError(err)
	defer func() { _ = response.Body.Close() }()
	data, err := ioutil.ReadAll(response.Body)
	fatalOnError(err)
	fmt.Printf("Read %d bytes remote YAML data from %s\n", len(data), yamlRemotePath)
	return data
}

// mapCompanyName: maps company name to possibly new company name (when one was acquired by the another)
// If mapping happens, store it in the cache for speed
// stat:
// --- [no_regexp_match, cache] (unmapped)
// Company_name [match_regexp, match_cache]
func mapCompanyName(comMap map[string][2]string, acqMap map[*regexp.Regexp]string, stat map[string][2]int, company string) string {
	res, ok := comMap[company]
	if ok {
		if res[1] == "m" {
			ary := stat[res[0]]
			ary[1]++
			stat[res[0]] = ary
		} else {
			ary := stat["---"]
			ary[1]++
			stat["---"] = ary
		}
		return res[0]
	}
	for re, res := range acqMap {
		if re.MatchString(company) {
			comMap[company] = [2]string{res, "m"}
			ary := stat[res]
			ary[0]++
			stat[res] = ary
			return res
		}
	}
	comMap[company] = [2]string{company, "u"}
	ary := stat["---"]
	ary[0]++
	stat["---"] = ary
	return company
}

func mapOrganization(db *sql.DB, companyName, lCompanyName string, mapOrgNames *allMappings, cache map[string]string, missingOrgs map[string]int, thrN int) string {
	mappedCompany, ok := cache[lCompanyName]
	if !ok {
		q := "select ? regexp ?"
		f := func(ch chan string, mp [2]string) {
			re := strings.Replace(mp[0], "\\\\", "\\", -1)
			rows, err := db.Query(q, lCompanyName, re)
			fatalOnError(err)
			match := 0
			for rows.Next() {
				fatalOnError(rows.Scan(&match))
				break
			}
			fatalOnError(rows.Err())
			fatalOnError(rows.Close())
			if match == 1 {
				to := mp[1]
				ch <- to
				//fmt.Printf("'%s' matches? '%s' -> %d\n", lCompanyName, re, match)
			} else {
				ch <- ""
			}
		}
		ch := make(chan string)
		nThreads := 0
		mappedCompany := ""
		for _, mp := range mapOrgNames.Mappings {
			go f(ch, mp)
			nThreads++
			if nThreads == thrN {
				mapped := <-ch
				nThreads--
				if mapped != "" {
					mappedCompany = mapped
					break
				}
			}
		}
		for nThreads > 0 {
			mapped := <-ch
			nThreads--
			if mappedCompany == "" && mapped != "" {
				mappedCompany = mapped
			}
		}
		if mappedCompany != "" {
			//fmt.Printf("Found mapping '%s' -> '%s'\n", companyName, mappedCompany)
			cache[lCompanyName] = mappedCompany
			return mappedCompany
		}
	} else {
		/*
			if mappedCompany != "" {
				fmt.Printf("Cached mapping '%s' -> '%s'\n", companyName, mappedCompany)
			} else {
				fmt.Printf("Cached miss for '%s'\n", lCompanyName)
			}
		*/
		return mappedCompany
	}
	//fmt.Printf("Can't find anything for '%s'\n", lCompanyName)
	n, _ := missingOrgs[companyName]
	missingOrgs[companyName] = n + 1
	cache[lCompanyName] = ""
	return ""
}

func genRenames(db *sql.DB, users *gitHubUsers, acqs *allAcquisitions, mapOrgNames *allMappings) {
	var re *regexp.Regexp
	acqMap := make(map[*regexp.Regexp]string)
	comMap := make(map[string][2]string)
	stat := make(map[string][2]int)
	srcMap := make(map[string]string)
	resMap := make(map[string]struct{})
	idxMap := make(map[*regexp.Regexp]int)
	noAcqs := os.Getenv("NO_ACQS") != ""
	trunc := 0
	if os.Getenv("TRUNC") != "" {
		var e error
		trunc, e = strconv.Atoi(os.Getenv("TRUNC"))
		fatalOnError(e)
	}
	if noAcqs {
		acqs.Acquisitions = [][2]string{}
	}
	for idx, acq := range acqs.Acquisitions {
		re = regexp.MustCompile(acq[0])
		res, ok := srcMap[acq[0]]
		if ok {
			fatalf("Acquisition number %d '%+v' is already present in the mapping and maps into '%s'", idx, acq, res)
		}
		srcMap[acq[0]] = acq[1]
		_, ok = resMap[acq[1]]
		if ok {
			fatalf("Acquisition number %d '%+v': some other acquisition already maps into '%s', merge them", idx, acq, acq[1])
		}
		resMap[acq[1]] = struct{}{}
		acqMap[re] = acq[1]
		idxMap[re] = idx
	}
	for re, res := range acqMap {
		i := idxMap[re]
		for idx, acq := range acqs.Acquisitions {
			if re.MatchString(acq[1]) && i != idx {
				fatalf("Acquisition's number %d '%s' result '%s' matches other acquisition number %d '%s' which maps to '%s', simplify it: '%v' -> '%s'", idx, acq[0], acq[1], i, re, res, acq[0], res)
			}
			if re.MatchString(acq[0]) && res != acq[1] {
				fatalf("Acquisition's number %d '%s' regexp '%s' matches other acquisition number %d '%s' which maps to '%s': result is different '%s'", idx, acq, acq[0], i, re, res, acq[1])
			}
		}
	}
	companies := make(map[string]int)
	nUsr := len(*users)
	for ui, user := range *users {
		if ui > 0 && ui%10000 == 0 {
			fmt.Printf("Processing JSON %d/%d\n", ui, nUsr)
		}
		if trunc > 0 && ui >= trunc {
			break
		}
		// MODE
		affs := user.Affiliation
		// affs, _ := user["affiliation"].(string)
		if affs == "NotFound" || affs == "(Unknown)" || affs == "?" || affs == "-" || affs == "" {
			continue
		}
		affsAry := strings.Split(affs, ", ")
		for _, aff := range affsAry {
			ary := strings.Split(aff, " < ")
			company := strings.TrimSpace(ary[0])
			if company == "" {
				continue
			}
			// Map using companies acquisitions/company names mapping
			company = mapCompanyName(comMap, acqMap, stat, company)
			n, _ := companies[company]
			companies[company] = n + 1
		}
	}
	// fmt.Printf("companies: %+v\nnumber of companies: %d\n", companies, len(companies))
	fmt.Printf("Number of companies: %d\n", len(companies))

	cache := make(map[string]string)
	missingOrgs := make(map[string]int)
	maps := make(map[string]string)
	ci := 0
	nComps := len(companies)
	miss := 0
	thrN := runtime.NumCPU()
	thrN /= 4
	if thrN < 1 {
		thrN = 1
	}
	runtime.GOMAXPROCS(thrN)
	replacer := strings.NewReplacer(`"`, "", "<", "", ",", "")
	for company := range companies {
		ci++
		if company == "" {
			continue
		}
		if ci > 0 && ci%200 == 0 {
			fmt.Printf("Processed %d/%d companies\n", ci, nComps)
		}
		lCompany := strings.ToLower(company)
		mappedName := mapOrganization(db, company, lCompany, mapOrgNames, cache, missingOrgs, thrN)
		if mappedName == "" {
			miss++
			continue
		}
		if mappedName == "Individual - No Account" {
			mappedName = "Independent"
		}
		if mappedName != company {
			// " < , cannot be used in affiliation property in github_users.json
			mappedName := replacer.Replace(mappedName)
			if mappedName != company {
				maps[company] = mappedName
				fmt.Printf("mappedName: '%s' -> '%s'\n", company, mappedName)
			}
		}
	}
	if miss > 0 {
		fmt.Printf("Missing %d orgs\n", miss)
	}
	fmt.Printf("Actual mappings made: %d\n", len(maps))
	//for from, to := range maps {
	//	fmt.Printf("'%s' -> '%s'\n", from, to)
	//}
	m := make(map[int][]string)
	for org, n := range companies {
		entry, ok := m[n]
		if ok {
			m[n] = append(entry, org)
		} else {
			m[n] = []string{org}
		}
	}
	ks := []int{}
	for k := range m {
		ks = append(ks, k)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ks)))
	s := ""
	for _, n := range ks {
		orgs := m[n]
		for _, org := range orgs {
			to, ok := maps[org]
			if ok {
				// to = replacer.Replace(to)
				fmt.Printf("%d times: '%s' -> '%s'\n", n, org, to)
				s += fmt.Sprintf("%s -> %s\n", org, to)
			}
		}
	}
	fmt.Printf("=============================>\n%s\n", s)
	noWrite := os.Getenv("NO_WRITE") != ""
	if noWrite {
		return
	}
	for ui, user := range *users {
		if ui > 0 && ui%10000 == 0 {
			fmt.Printf("Processing JSON %d/%d\n", ui, nUsr)
		}
		if trunc > 0 && ui >= trunc {
			break
		}
		// MODE
		affs := user.Affiliation
		// affs, _ := user["affiliation"].(string)
		if affs == "NotFound" || affs == "(Unknown)" || affs == "?" || affs == "-" || affs == "" {
			continue
		}
		affsAry := strings.Split(affs, ", ")
		replaces := [][2]string{}
		for _, aff := range affsAry {
			ary := strings.Split(aff, " < ")
			company := strings.TrimSpace(ary[0])
			if company == "" {
				continue
			}
			mappedCompany, mapped := maps[company]
			if mapped {
				replaces = append(replaces, [2]string{company, mappedCompany})
			}
		}
		if len(replaces) > 0 {
			for _, replace := range replaces {
				affs = strings.Replace(affs, replace[0], replace[1], -1)
			}
			// fmt.Printf("'%s' --> '%s'\n", user.Affiliation, affs)
			// MODE
			(*users)[ui].Affiliation = affs
			// (*users)[ui]["affiliation"] = affs
		}
	}
	var js = json.Config{
		EscapeHTML:  false,
		SortMapKeys: gSortKeys,
	}.Froze()
	pretty, err := js.MarshalIndent(&users, "", "  ")
	fatalOnError(err)
	fatalOnError(ioutil.WriteFile("mapped.json", pretty, 0644))
	/*
		bf := bytes.NewBuffer([]byte{})
		js := json.NewEncoder(bf)
		js.SetEscapeHTML(false)
		js.SetSortMaps(false)
		js.SetIndent("", "  ")
		fatalOnError(js.Encode(&users))
		fatalOnError(ioutil.WriteFile("mapped.json", bf.Bytes(), 0644))
	*/
}

func mapOrgs() {
	// Connect to MariaDB
	dsn := getConnectString()
	db, err := sql.Open("mysql", dsn)
	fatalOnError(err)
	db.SetMaxIdleConns(128)
	db.SetMaxOpenConns(128)
	dur, _ := time.ParseDuration("10m")
	db.SetConnMaxLifetime(dur)
	defer func() { fatalOnError(db.Close()) }()

	// Parse github_users.json
	var users gitHubUsers
	// Read json data from local file falling back to remote file
	data := getAffiliationsJSONBody()
	fatalOnError(json.Unmarshal(data, &users))

	// Parse companies.yaml
	var acqs allAcquisitions
	// Read yaml data from local file falling back to remote file
	data = getAcquisitionsYAMLBody()
	fatalOnError(yaml.Unmarshal(data, &acqs))

	// Parse DA's map_org_names.yaml
	var mapOrgNames allMappings
	// Read yaml data from local file falling back to remote file
	data = getMapOrgNamesYAMLBody()
	fatalOnError(yaml.Unmarshal(data, &mapOrgNames))

	// Generate matching renames
	genRenames(db, &users, &acqs, &mapOrgNames)
}

func main() {
	mapOrgs()
}
