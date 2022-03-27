package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"

	"github.com/spf13/viper"
)

// NewProxy takes target host and creates a reverse proxy, for now just the single 1
func NewProxy(targetHost string) (*httputil.ReverseProxy, error) {
	url, err := url.Parse(targetHost)
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(url)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		modifyRequest(req)
	}

	proxy.ModifyResponse = modifyResponse()
	proxy.ErrorHandler = errorHandler()
	return proxy, nil
}

// so we can identify requests through this thing in the logs on the backend
func modifyRequest(req *http.Request) {
	log.Printf("rewrote request header")
	req.Header.Set(viper.GetString("header.request.name"), viper.GetString("header.request.value"))
}

func errorHandler() func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, req *http.Request, err error) {
		log.Printf("Got error while modifying response: %v \n", err)
		return
	}
}

func zippedReadAll(r io.Reader) ([]byte, error) {
	reader, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	buff, err := ioutil.ReadAll(reader)
	return buff, err
}

func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func isGzipped(header map[string][]string) bool {
	headerEncoding, headerExists := header["Content-Encoding"]
	if headerExists {
		fmt.Println("encoding header: ", headerEncoding)
		if stringInSlice("gzip", headerEncoding) {
			return true
		} else {
			return false
		}
	}
	return false
}

func rewriteBody(b []byte) ([]byte, bool) {
	changed := false
	for k, v := range viper.GetStringMap("pattern.items") {
		log.Printf("%s:\n", k)
		childmap, _ := v.(map[string]interface{})
		re := regexp.MustCompile(fmt.Sprint(childmap["source"]))
		if re.Match(b) {
			log.Printf("\tmatch(es) found for: %s\n", k)
			b = re.ReplaceAll(b, []byte(fmt.Sprint(childmap["target"])))
			changed = true
		}
	}
	return b, changed
}

func modifyResponse() func(*http.Response) error {
	return func(resp *http.Response) error {
		log.Printf("rewriting response header")
		log.Println(resp.Header)
		gzipped := isGzipped(resp.Header)
		var b []byte
		if gzipped {
			b, _ = zippedReadAll(resp.Body)
		} else {
			b, _ = ioutil.ReadAll(resp.Body)
		}

		resp.Header.Set(viper.GetString("header.response.name"), viper.GetString("header.response.value"))

		d, changed := rewriteBody(b)

		if changed {
			b = d
		}

		if gzipped {
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			gz.Write(b)
			gz.Close()
			b = buf.Bytes()
		}

		resp.Body = ioutil.NopCloser(bytes.NewReader(b))
		resp.ContentLength = int64(len(b))
		resp.Header.Set("Content-Length", strconv.Itoa(len(b)))

		return nil
	}
}

func ProxyRequestHandler(proxy *httputil.ReverseProxy) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	}
}

func init() {
	viper.SetConfigName("config") // can be json or yaml (or ini, ...), but prefer yaml for the sake of comments
	viper.AddConfigPath("../config")
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			// Config file not found
			log.Println("no such config file")
		} else {
			// Config file was found but another error was produced
			log.Println("read config error")
		}
		log.Fatal(err) // failed to read configuration file.
	}
	log.Printf("### Starting up ###")
	log.Printf("config file used: %s", viper.ConfigFileUsed())
}

func main() {
	// initialize the reverse proxy and pass the actual backend server url here
	backend := fmt.Sprintf("%s://%s:%d", viper.GetString("backend.schema"), viper.GetString("backend.host"), viper.GetInt("backend.port"))
	log.Printf("Setting up backend to: %s\n", backend)
	proxy, err := NewProxy(backend)
	if err != nil {
		panic(err)
	}

	// handle all requests to your server using the proxy
	http.HandleFunc("/", ProxyRequestHandler(proxy))
	log.Printf("listening on port :%s", viper.GetString("frontend.port"))
	log.Fatal(http.ListenAndServe(":"+viper.GetString("frontend.port"), nil))
}
