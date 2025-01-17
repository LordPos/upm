package haskell

import (
    "encoding/json"
    "net/http"
    "net/url"
    "io/ioutil"
    "regexp"
    "github.com/replit/upm/internal/api"
    "github.com/replit/upm/internal/util"
    "strings"
    "os"
    "fmt"
)

var HaskellBackend = api.LanguageBackend {
    Name:                "haskell-stack",
    Specfile:            "project.cabal",
    Lockfile:            "stack.yaml",
    // also present: a package-name.cabal file, a stack.yaml.lock that stores hashes
    FilenamePatterns:    []string{"*.hs"},
    Quirks:              api.QuirksAddRemoveAlsoLocks,
    GetPackageDir:       func () string {
        return ".stack-work"
    },
    // Stack actually uses two package directories:
    // ~/.stack/ for stackage packages and .stack-work for others
    Search:              func(q string) []api.PkgInfo {
        return searchFunction(q,false)
    },
    Info:                func(i api.PkgName) api.PkgInfo {
        return searchFunction(string(i),true)[0]
    },
    Add:                 Add,
    Remove:              Remove,
    // Stack's baseline philosophy is that build plans are always reproducible.
    // Which means locking is automatic. Versions are precise. This spares a lot of spec trouble.
    // https://docs.haskellstack.org/en/stable/stack_yaml_vs_cabal_package_file/
    Lock:                func(){},
    Install:             func(){
        util.RunCmd([]string{"stack", "build", "--dependencies-only"})
    },
    // lock will be used for non-stackage packages and spec for stackage ones
    ListSpecfile:        ListSpecfile,
    ListLockfile:        ListLockfile,
    Guess:               func()(map[api.PkgName]bool,bool){
        util.Die("not implemented")
        return nil,false
    },

    
}

func searchFunction(query string, indiv bool) []api.PkgInfo {
    // search stackage[hoogle] first
    res, err:= http.Get("https://hoogle.haskell.org/?mode=json&format=text&hoogle="+url.QueryEscape(query)+"%20is%3Apackage")
    if err!= nil {
        util.Die("hoogle response:"+ err.Error())
    }
    var results = []map[string]string{}
    data, _ := ioutil.ReadAll(res.Body)
    res.Body.Close()
    json.Unmarshal(data, &results)
    finresults := []api.PkgInfo{}
    if indiv {results = results[:1]} // so we can skip implementing the search function again
    for _,result := range results{
        if result["item"] == ""{
            continue
        }
        getCabal, err := http.Get(result["url"] + "/src/" + result["item"][8:] + ".cabal")
        if err!= nil {
            util.Die("hackage [cabal file] response:" + err.Error())
        }
        cabal, _ := ioutil.ReadAll(getCabal.Body)
        getCabal.Body.Close()
        cabalRegexp := regexp.MustCompile(`(\S+): +(.+)`)
        cabalDataSlice := cabalRegexp.FindAllSubmatch(cabal, -1)
        cabalData := make(map[string]string)
        for _,s := range cabalDataSlice{
            cabalData[string(s[1])] = string(s[2])
        }
        info := api.PkgInfo{
            Name:           result["item"][8:],
            Description:    strings.ReplaceAll(result["docs"], "\n", ""),
            Version:        cabalData["version"],
            HomepageURL:    result["url"] ,
            SourceCodeURL:  cabalData["homepage"], 
            // homepage tends to be the github page 
            BugTrackerURL:  cabalData["bug-reports"],
            Author:         cabalData["author"],
            // sometimes this is author <email>
            // other times it's just author and the email is in cabalData["maintainer"]
            // possible todo ^^
            License:        cabalData["license"],
            // will implement Dependencies later
        }
        finresults = append(finresults,info)
    }
    if indiv && (len(finresults) == 0 || finresults[0].Name != query){
        util.Die("cannot find package " + query)
    }
    return finresults
}


var initialCabal = 
`name:                project
version:             0.0.0
build-type:          Simple
cabal-version:       >=1.10

executable main
  hs-source-dirs:      .
  main-is:             main.hs
  default-language:    Haskell2010
  build-depends:       
    base >= 4.7 && < 5
`

//TODO: more sophisticated cabal/yaml parsing
func Add(packages map[api.PkgName]api.PkgSpec, projectName string){
    if _,err := os.Open("./project.cabal"); os.IsNotExist(err) {
        file, _ := os.Create("./project.cabal")
        file.Write([]byte(initialCabal))
        file.Close()
        file, _ = os.Create("./stack.yaml")
        file.Write([]byte("resolver: lts-16.10\nsystem-ghc: true\n"))
        // the system-ghc flag *allows* stack to use system ghc if version required by resolver and system ghc version match.
        // 16.10 is the latest ltc at the time of writing. ghc version 8.8.3 preferred.
        // default behaviour is to download the necessary version of ghc on the fly, which consumes both time and memory
        file.Close()
    }
    // the canon way to add dependencies to stack projects is to manually write them in the cabal file.
    // non-stackage packages should be mentioned with version in the stack.yaml file
    for name, version := range(packages){
        packageInfo := searchFunction(string(name),true)[0]
        onStackage := !strings.Contains(string(packageInfo.Description), "Not on Stackage")
        if !onStackage {
            if contents,_ := ioutil.ReadFile("stack.yaml"); !strings.Contains(string(contents),"extra-deps"){
                f, _ := os.OpenFile("stack.yaml", os.O_APPEND | os.O_WRONLY, 0644)
                f.Write([]byte("extra-deps:\n"))
                f.Close()
            }
            if version == ""{
                version = api.PkgSpec(packageInfo.Version)
            }
            f, _ := os.OpenFile("stack.yaml", os.O_APPEND | os.O_WRONLY, 0644)
            f.WriteString("- " + string(name) + "-" + string(version) + "\n")
            f.Close()     
        } 
        f, _ := os.OpenFile("project.cabal", os.O_APPEND | os.O_WRONLY, 0644)
        f.WriteString("    , " + string(name) + "\n" )
        f.Close() 
    }
}
    
     
func ListSpecfile() map[api.PkgName]api.PkgSpec {
    // spec is simply an empty string
    // while cabal does allow </>/= specification, it won't be needed because stack
    specRegex := regexp.MustCompile(`    , (.+)`)
    contents, _ := ioutil.ReadFile("project.cabal")
    matches := specRegex.FindAllSubmatch(contents, -1)
    packages := make(map[api.PkgName]api.PkgSpec)
    for _,match := range matches {
        packages[api.PkgName(match[1])] = ""
    }
    return packages
}

func ListLockfile() map[api.PkgName]api.PkgVersion{
    lockRegex := regexp.MustCompile(`- (.+)`)
    contents, _ := ioutil.ReadFile("stack.yaml")
    matches := lockRegex.FindAllSubmatch(contents, -1)
    packages := make(map[api.PkgName]api.PkgVersion)
    for _,match := range matches{
        l := strings.Split(string(match[1]), "-")
        packages[api.PkgName(strings.Join(l[:len(l)-1],"-"))] = api.PkgVersion(l[len(l)-1])
    }
    return packages
}

func Remove(pkgs map[api.PkgName]bool){
    nonStackage := ListLockfile()
    spec,_ := ioutil.ReadFile("project.cabal")
    lock,_ := ioutil.ReadFile("stack.yaml")
    specFile,_ := os.OpenFile("project.cabal", os.O_WRONLY | os.O_TRUNC, 0644)
    lockFile,_ := os.OpenFile("stack.yaml", os.O_WRONLY | os.O_TRUNC, 0644)
    for name := range pkgs{
        if val,ok := nonStackage[name]; ok{           
            lock = []byte(strings.Replace(string(lock),"- "+string(name)+"-"+string(val)+"\n","",-1))
            fmt.Println("- "+string(name)+"-"+string(val)+"\n")
        }
        spec = []byte(strings.Replace(string(spec),"    , "+string(name)+"\n", "",-1))
    }
    specFile.Write(spec)
    lockFile.Write(lock)
    return
}