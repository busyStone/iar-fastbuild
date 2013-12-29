package main

import (
	"bytes"
	"code.google.com/p/go-charset/charset"
	_ "code.google.com/p/go-charset/data"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/bitly/go-simplejson"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
	//"time"
)

// xml -> struct
type EWW struct {
	EwpList []string `xml:"project>path"`
}

func abort(funcname string, err error) {
	panic(funcname + " failed: " + err.Error())
}

// 从 max 中找到 name 文件
// name 仅为文件名且无后缀
func findFile(name string, max []string) (filePath string, err error) {

	for _, v := range max {
		_, file := path.Split(v)

		if name+".ewp" == file {
			return v, nil
		}
	}

	return "", errors.New("Can't find file " + name + "in .ewp")
}

// 获取剩余文件
// except 中文件为带路径文件  与 max 格式一致
// left 中文件为带路径文件  与 max 格式一致
func leftFile(except []string, max []string) (left []string, err error) {

	if len(except) == 0 {
		copy(max, left)
	} else {
	maxRange:
		for _, v1 := range max {
			for _, v2 := range except {
				if v1 == v2 {
					continue maxRange
				}
			}

			left = append(left, v1)
		}
	}

	return left, nil
}

// 解析 .eww 文件(xml格式)
// 返回 .ewp 文件列表（包含路径）
func parseEWW2List(filePath string) ([]string, error) {

	content, err := ioutil.ReadFile(filePath)
	if err != nil {
		abort("ioutil.ReadFile("+filePath+")", err)
	}
	/*
		data := `
		    <workspace>
		        <project>
		            <path>"path test 1"</path>
		        </project>
		    </workspace>`
	*/
	eww := EWW{}

	d := xml.NewDecoder(bytes.NewBuffer(content))
	d.CharsetReader = charset.NewReader
	err = d.Decode(&eww)
	if err != nil {
		abort("xml.Unmarshal()", err)
	}
	/*
		for _, v := range eww.EwpList {
			fmt.Println(v)
		}
	*/

	// 替换 \ -> /
	// 替换 $WS_DIR$ -> 实际路径
	ewwPath, _ := path.Split(strings.Replace(filePath, "\\", "/", -1))

	for k, _ := range eww.EwpList {
		eww.EwpList[k] = strings.Replace(eww.EwpList[k], "\\", "/", -1)
		eww.EwpList[k] = strings.Replace(eww.EwpList[k], "$WS_DIR$/", ewwPath, -1)
	}
	return eww.EwpList, nil
}

// 根据 cfg.json 及解析规则
// 1 pre 先编译且串行编译
// 2 post 最后编译且串行编译
// 3 parall pre 之后 post 之前 并行编译
// 4 except 中所列工程 不编译（不管是否在 pre、parall、post 中出现）
// 5 如果 parall为空 则parall范围为 除 pre、post、except之外所有在.eww中能够找到的文件
func splitEwpList(ewpList []string,
	pre []interface{},
	parall []interface{},
	post []interface{},
	except []interface{}) (preList []string, parallList []string, postList []string, err error) {

	// 得到有效文件列表
	var exceptList []string

	for _, v := range except {
		file, err := findFile(v.(string), ewpList)
		if err == nil {
			exceptList = append(exceptList, file)
		}
	}

	validList, _ := leftFile(exceptList, ewpList)
	//fmt.Println(validList)

	// pre build List
	for _, v := range pre {
		file, err := findFile(v.(string), validList)
		if err == nil {
			preList = append(preList, file)
		}
	}

	// post build List
	for _, v := range post {
		file, err := findFile(v.(string), validList)
		if err == nil {
			postList = append(postList, file)
		}
	}

	// 如果 parall为空 则 所有不属于 pre、post和except的均属于parall
	if len(parall) != 0 {
		for _, v := range parall {
			file, err := findFile(v.(string), validList)
			if err == nil {
				parallList = append(parallList, file)
			}
		}
	} else {
		for _, v := range preList {
			exceptList = append(exceptList, v)
		}
		for _, v := range postList {
			exceptList = append(exceptList, v)
		}

		parallList, _ = leftFile(exceptList, validList)
	}

	return preList, parallList, postList, nil
}

func sbuild(cmd string, ewpList []string, optbuild string, cfg string, optlog string) (info []string, err error) {
	for _, v := range ewpList {
		fmt.Println(v)
		execCmd := exec.Command(cmd, v, optbuild, cfg, "-log", optlog)
		err = execCmd.Run()
		if err != nil {
			info = append(info, v+" "+optbuild+" failed!")
		}
	}

	if len(info) != 0 {
		err = errors.New("Some .ewp build failed!")
	}

	return info, err
}

func pbuild(cmd string, ewpList []string, optbuild string, cfg string, optlog string) (info []string, err error) {

	c := make(chan string, len(ewpList))

	for _, v := range ewpList {
		go func(c chan string, cmd, v, optbuild, cfg, optlog string) {
			fmt.Println(v)
			execCmd := exec.Command(cmd, v, optbuild, cfg, "-log", optlog)
			err := execCmd.Run()

			var infostr string
			if err != nil {
				infostr = v + " " + optbuild + " failed!"
			} else {
				infostr = "Done"
			}

			c <- infostr
		}(c, cmd, v, optbuild, cfg, optlog)
	}

	for i := 0; i < len(ewpList); i++ {
		errstr := <-c

		if errstr != "Done" {
			info = append(info, errstr)
		}
	}

	if len(info) != 0 {
		err = errors.New("Some .ewp build failed!")
	}

	return info, err
}

func main() {

	// 异常处理
	defer func() {
		if err := recover(); err != nil {
			fmt.Println(err)

			// 中止运行
			fmt.Scanln()
		}
	}()

	// 解析JSON
	file, err := os.Open("cfg.json")
	if err != nil {
		abort("os.Open(cfg.json", err)
	}

	defer file.Close()

	fileinfo, err := file.Stat()
	if err != nil {
		abort("file.Stat()", err)
	}

	//fmt.Println(fileinfo.Size())
	body := make([]byte, fileinfo.Size())

	file.Read(body)
	//fmt.Println(p)

	js, err := simplejson.NewJson(body)
	if err != nil {
		abort("simplejson.NewJson", err)
	}
	/*
		fmt.Println(js.Get("comment"))

		fmt.Println(js.Get("exe").Get("comment"))
		fmt.Println(js.Get("exe").Get("path"))

		fmt.Println(js.Get("eww").Get("comment"))
		fmt.Println(js.Get("eww").Get("path"))

		fmt.Println(js.Get("ewp").Get("comment"))
		fmt.Println(js.Get("ewp").Get("pre"))
		fmt.Println(js.Get("ewp").Get("parall"))
		fmt.Println(js.Get("ewp").Get("post"))
	*/

	// 设定可用于工作的CPU个数
	num, err := js.Get("cpunum").Float64()
	if err != nil {
		abort("js.Get(\"cpunum\").Float64()", err)
	}

	cpunum := int(num)
	realnum := runtime.NumCPU()

	if cpunum == 0 || cpunum >= realnum {
		if realnum == 1 {
			cpunum = realnum
		} else {
			cpunum = realnum - 1
		}
	}

	runtime.GOMAXPROCS(cpunum)

	// 读取 eww 文件 获取工程列表
	ewwPath, err := js.Get("eww").Get("path").String()
	if err != nil {
		abort("js.Get(\"eww\").Get(\"path\").String()", err)
	}

	ewpList, err := parseEWW2List(ewwPath)
	if err != nil {
		abort("parseEWW2List", err)
	}

	preArray, err := js.Get("ewp").Get("pre").Array()
	if err != nil {
		abort("js.Get(\"ewp\").Get(\"pre\").Array()", err)
	}
	parallArray, err := js.Get("ewp").Get("parall").Array()
	if err != nil {
		abort("js.Get(\"ewp\").Get(\"parall\").Array()", err)
	}
	postArray, err := js.Get("ewp").Get("post").Array()
	if err != nil {
		abort("js.Get(\"ewp\").Get(\"post\").Array()", err)
	}
	exceptArray, err := js.Get("ewp").Get("except").Array()
	if err != nil {
		abort("js.Get(\"ewp\").Get(\"except\").Array()", err)
	}

	preList, parallList, postList, err := splitEwpList(ewpList, preArray, parallArray, postArray, exceptArray)
	if err != nil {
		abort("splitEwpList", err)
	}
	/*
		fmt.Println(preList)
		fmt.Println(parallList)
		fmt.Println(postList)
	*/

	cmd, err := js.Get("exe").Get("path").String()
	if err != nil {
		abort("js.Get(\"exe\").Get(\"path\").String()", err)
	}

	cfg, err := js.Get("cfg").Get("ver").String()
	if err != nil {
		abort("js.Get(\"cfg\").Get(\"ver\").String()", err)
	}
	optbuild, err := js.Get("option").Get("proc").String()
	if err != nil {
		abort("js.Get(\"option\").Get(\"proc\").String()", err)
	}
	optlog, err := js.Get("option").Get("log").String()
	if err != nil {
		abort("js.Get(\"option\").Get(\"log\").String()", err)
	}

	if cfg == "" {
		cfg = "Debug"
	}
	if optbuild == "" {
		optbuild = "-make"
	}
	if optlog == "" {
		optlog = "errors"
	}

	// 是否需要 -clean
	// -build 默认需要先 -clean
	if optbuild == "-build" {
		//t1 := time.Now()
		logerr, err := sbuild(cmd, preList, "-clean", cfg, optlog)
		if err != nil {
			fmt.Println(logerr)
		}
		logerr, err = pbuild(cmd, parallList, "-clean", cfg, optlog)
		if err != nil {
			fmt.Println(logerr)
		}
		logerr, err = sbuild(cmd, postList, "-clean", cfg, optlog)
		if err != nil {
			fmt.Println(logerr)
		}
		//fmt.Println(time.Now().Sub(t1).String())
	}

	// 编译
	// sbuild preList 2m39s
	// sbuild parallList 2m53s
	// sbuild postList 6.7s
	// pbuild parallList 6m!!!!
	//t2 := time.Now()
	logerr, err := sbuild(cmd, preList, optbuild, cfg, optlog)
	if err != nil {
		fmt.Println(logerr)
	}
	logerr, err = sbuild(cmd, parallList, optbuild, cfg, optlog)
	if err != nil {
		fmt.Println(logerr)
	}
	logerr, err = sbuild(cmd, postList, optbuild, cfg, optlog)
	if err != nil {
		fmt.Println(logerr)
	}
	//fmt.Println(time.Now().Sub(t2).String())

}
