/*
 * Minio Cloud Storage, (C) 2016 Minio, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	jwtgo "github.com/dgrijalva/jwt-go"
	"github.com/dustin/go-humanize"
	"github.com/gorilla/mux"
	"github.com/gorilla/rpc/v2/json2"
	"github.com/minio/minio/pkg/disk"
	"github.com/minio/minio/pkg/fs"
	"github.com/minio/miniobrowser"
)

// isJWTReqAuthenticated validates if any incoming request to be a
// valid JWT authenticated request.
func isJWTReqAuthenticated(req *http.Request) bool {
	jwt := initJWT()
	token, e := jwtgo.ParseFromRequest(req, func(token *jwtgo.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwtgo.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("Unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(jwt.SecretAccessKey), nil
	})
	if e != nil {
		return false
	}
	return token.Valid
}

// GenericArgs - empty struct for calls that don't accept arguments
// for ex. ServerInfo, GenerateAuth
type GenericArgs struct{}

// GenericRep - reply structure for calls for which reply is success/failure
// for ex. RemoveObject MakeBucket
type GenericRep struct {
	UIVersion string `json:"uiVersion"`
}

// ServerInfoRep - server info reply.
type ServerInfoRep struct {
	MinioVersion  string
	MinioMemory   string
	MinioPlatform string
	MinioRuntime  string
	UIVersion     string `json:"uiVersion"`
}

// ServerInfo - get server info.
func (web *webAPI) ServerInfo(r *http.Request, args *GenericArgs, reply *ServerInfoRep) error {
	if !isJWTReqAuthenticated(r) {
		return &json2.Error{Message: "Unauthorized request"}
	}
	host, err := os.Hostname()
	if err != nil {
		host = ""
	}
	memstats := &runtime.MemStats{}
	runtime.ReadMemStats(memstats)
	mem := fmt.Sprintf("Used: %s | Allocated: %s | Used-Heap: %s | Allocated-Heap: %s",
		humanize.Bytes(memstats.Alloc),
		humanize.Bytes(memstats.TotalAlloc),
		humanize.Bytes(memstats.HeapAlloc),
		humanize.Bytes(memstats.HeapSys))
	platform := fmt.Sprintf("Host: %s | OS: %s | Arch: %s",
		host,
		runtime.GOOS,
		runtime.GOARCH)
	goruntime := fmt.Sprintf("Version: %s | CPUs: %s", runtime.Version(), strconv.Itoa(runtime.NumCPU()))
	reply.MinioVersion = minioVersion
	reply.MinioMemory = mem
	reply.MinioPlatform = platform
	reply.MinioRuntime = goruntime
	reply.UIVersion = miniobrowser.UIVersion
	return nil
}

// DiskInfoRep - disk info reply.
type DiskInfoRep struct {
	DiskInfo  disk.Info `json:"diskInfo"`
	UIVersion string    `json:"uiVersion"`
}

// DiskInfo - get disk statistics.
func (web *webAPI) DiskInfo(r *http.Request, args *GenericArgs, reply *DiskInfoRep) error {
	if !isJWTReqAuthenticated(r) {
		return &json2.Error{Message: "Unauthorized request"}
	}
	info, e := disk.GetInfo(web.Filesystem.GetRootPath())
	if e != nil {
		return &json2.Error{Message: e.Error()}
	}
	reply.DiskInfo = info
	reply.UIVersion = miniobrowser.UIVersion
	return nil
}

// MakeBucketArgs - make bucket args.
type MakeBucketArgs struct {
	BucketName string `json:"bucketName"`
}

// MakeBucket - make a bucket.
func (web *webAPI) MakeBucket(r *http.Request, args *MakeBucketArgs, reply *GenericRep) error {
	if !isJWTReqAuthenticated(r) {
		return &json2.Error{Message: "Unauthorized request"}
	}
	reply.UIVersion = miniobrowser.UIVersion
	e := web.Filesystem.MakeBucket(args.BucketName)
	if e != nil {
		return &json2.Error{Message: e.Cause.Error()}
	}
	return nil
}

// ListBucketsRep - list buckets response
type ListBucketsRep struct {
	Buckets   []BucketInfo `json:"buckets"`
	UIVersion string       `json:"uiVersion"`
}

// BucketInfo container for list buckets metadata.
type BucketInfo struct {
	// The name of the bucket.
	Name string `json:"name"`
	// Date the bucket was created.
	CreationDate time.Time `json:"creationDate"`
}

// ListBuckets - list buckets api.
func (web *webAPI) ListBuckets(r *http.Request, args *GenericArgs, reply *ListBucketsRep) error {
	if !isJWTReqAuthenticated(r) {
		return &json2.Error{Message: "Unauthorized request"}
	}
	buckets, e := web.Filesystem.ListBuckets()
	if e != nil {
		return &json2.Error{Message: e.Cause.Error()}
	}
	for _, bucket := range buckets {
		// List all buckets which are not private.
		if bucket.Name != path.Base(reservedBucket) {
			reply.Buckets = append(reply.Buckets, BucketInfo{
				Name:         bucket.Name,
				CreationDate: bucket.Created,
			})
		}
	}
	reply.UIVersion = miniobrowser.UIVersion
	return nil
}

// ListObjectsArgs - list object args.
type ListObjectsArgs struct {
	BucketName string `json:"bucketName"`
	Prefix     string `json:"prefix"`
}

// ListObjectsRep - list objects response.
type ListObjectsRep struct {
	Objects   []ObjectInfo `json:"objects"`
	UIVersion string       `json:"uiVersion"`
}

// ObjectInfo container for list objects metadata.
type ObjectInfo struct {
	// Name of the object
	Key string `json:"name"`
	// Date and time the object was last modified.
	LastModified time.Time `json:"lastModified"`
	// Size in bytes of the object.
	Size int64 `json:"size"`
	// ContentType is mime type of the object.
	ContentType string `json:"contentType"`
}

// ListObjects - list objects api.
func (web *webAPI) ListObjects(r *http.Request, args *ListObjectsArgs, reply *ListObjectsRep) error {
	marker := ""
	if !isJWTReqAuthenticated(r) {
		return &json2.Error{Message: "Unauthorized request"}
	}
	for {
		lo, err := web.Filesystem.ListObjects(args.BucketName, args.Prefix, marker, "/", 1000)
		if err != nil {
			return &json2.Error{Message: err.Cause.Error()}
		}
		marker = lo.NextMarker
		for _, obj := range lo.Objects {
			reply.Objects = append(reply.Objects, ObjectInfo{
				Key:          obj.Name,
				LastModified: obj.ModifiedTime,
				Size:         obj.Size,
			})
		}
		for _, prefix := range lo.Prefixes {
			reply.Objects = append(reply.Objects, ObjectInfo{
				Key: prefix,
			})
		}
		if !lo.IsTruncated {
			break
		}
	}
	reply.UIVersion = miniobrowser.UIVersion
	return nil
}

// RemoveObjectArgs - args to remove an object
type RemoveObjectArgs struct {
	TargetHost string `json:"targetHost"`
	BucketName string `json:"bucketName"`
	ObjectName string `json:"objectName"`
}

// RemoveObject - removes an object.
func (web *webAPI) RemoveObject(r *http.Request, args *RemoveObjectArgs, reply *GenericRep) error {
	if !isJWTReqAuthenticated(r) {
		return &json2.Error{Message: "Unauthorized request"}
	}
	reply.UIVersion = miniobrowser.UIVersion
	e := web.Filesystem.DeleteObject(args.BucketName, args.ObjectName)
	if e != nil {
		return &json2.Error{Message: e.Cause.Error()}
	}
	return nil
}

// LoginArgs - login arguments.
type LoginArgs struct {
	Username string `json:"username" form:"username"`
	Password string `json:"password" form:"password"`
}

// LoginRep - login reply.
type LoginRep struct {
	Token     string `json:"token"`
	UIVersion string `json:"uiVersion"`
}

// Login - user login handler.
func (web *webAPI) Login(r *http.Request, args *LoginArgs, reply *LoginRep) error {
	jwt := initJWT()
	if jwt.Authenticate(args.Username, args.Password) {
		token, err := jwt.GenerateToken(args.Username)
		if err != nil {
			return &json2.Error{Message: err.Cause.Error(), Data: err.String()}
		}
		reply.Token = token
		reply.UIVersion = miniobrowser.UIVersion
		return nil
	}
	return &json2.Error{Message: "Invalid credentials"}
}

// GenerateAuthReply - reply for GenerateAuth
type GenerateAuthReply struct {
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	UIVersion string `json:"uiVersion"`
}

func (web webAPI) GenerateAuth(r *http.Request, args *GenericArgs, reply *GenerateAuthReply) error {
	if !isJWTReqAuthenticated(r) {
		return &json2.Error{Message: "Unauthorized request"}
	}
	cred := mustGenAccessKeys()
	reply.AccessKey = cred.AccessKeyID
	reply.SecretKey = cred.SecretAccessKey
	reply.UIVersion = miniobrowser.UIVersion
	return nil
}

// SetAuthArgs - argument for SetAuth
type SetAuthArgs struct {
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
}

// SetAuthReply - reply for SetAuth
type SetAuthReply struct {
	Token     string `json:"token"`
	UIVersion string `json:"uiVersion"`
}

// SetAuth - Set accessKey and secretKey credentials.
func (web *webAPI) SetAuth(r *http.Request, args *SetAuthArgs, reply *SetAuthReply) error {
	if !isJWTReqAuthenticated(r) {
		return &json2.Error{Message: "Unauthorized request"}
	}
	if args.AccessKey == "" {
		return &json2.Error{Message: "Empty access key not allowed"}
	}
	if args.SecretKey == "" {
		return &json2.Error{Message: "Empty secret key not allowed"}
	}
	cred := credential{args.AccessKey, args.SecretKey}
	serverConfig.SetCredential(cred)
	if err := serverConfig.Save(); err != nil {
		return &json2.Error{Message: err.Cause.Error()}
	}

	jwt := initJWT()
	if !jwt.Authenticate(args.AccessKey, args.SecretKey) {
		return &json2.Error{Message: "Invalid credentials"}
	}
	token, err := jwt.GenerateToken(args.AccessKey)
	if err != nil {
		return &json2.Error{Message: err.Cause.Error()}
	}
	reply.Token = token
	reply.UIVersion = miniobrowser.UIVersion
	return nil
}

// GetAuthReply - Reply current credentials.
type GetAuthReply struct {
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	UIVersion string `json:"uiVersion"`
}

// GetAuth - return accessKey and secretKey credentials.
func (web *webAPI) GetAuth(r *http.Request, args *GenericArgs, reply *GetAuthReply) error {
	if !isJWTReqAuthenticated(r) {
		return &json2.Error{Message: "Unauthorized request"}
	}
	creds := serverConfig.GetCredential()
	reply.AccessKey = creds.AccessKeyID
	reply.SecretKey = creds.SecretAccessKey
	reply.UIVersion = miniobrowser.UIVersion
	return nil
}

// Upload - file upload handler.
func (web *webAPI) Upload(w http.ResponseWriter, r *http.Request) {
	if !isJWTReqAuthenticated(r) {
		writeWebErrorResponse(w, errInvalidToken)
		return
	}
	vars := mux.Vars(r)
	bucket := vars["bucket"]
	object := vars["object"]
	if _, err := web.Filesystem.CreateObject(bucket, object, -1, r.Body, nil); err != nil {
		writeWebErrorResponse(w, err.ToGoError())
	}
}

// Download - file download handler.
func (web *webAPI) Download(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	bucket := vars["bucket"]
	object := vars["object"]
	token := r.URL.Query().Get("token")

	jwt := initJWT()
	jwttoken, e := jwtgo.Parse(token, func(token *jwtgo.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwtgo.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("Unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(jwt.SecretAccessKey), nil
	})
	if e != nil || !jwttoken.Valid {
		writeWebErrorResponse(w, errInvalidToken)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filepath.Base(object)))

	if _, err := web.Filesystem.GetObject(w, bucket, object, 0, 0); err != nil {
		writeWebErrorResponse(w, err.ToGoError())
	}
}

// writeWebErrorResponse - set HTTP status code and write error description to the body.
func writeWebErrorResponse(w http.ResponseWriter, err error) {
	if err == errInvalidToken {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(err.Error()))
		return
	}
	switch err.(type) {
	case fs.RootPathFull:
		apiErr := getAPIError(ErrRootPathFull)
		w.WriteHeader(apiErr.HTTPStatusCode)
		w.Write([]byte(apiErr.Description))
	case fs.BucketNotFound:
		apiErr := getAPIError(ErrNoSuchBucket)
		w.WriteHeader(apiErr.HTTPStatusCode)
		w.Write([]byte(apiErr.Description))
	case fs.BucketNameInvalid:
		apiErr := getAPIError(ErrInvalidBucketName)
		w.WriteHeader(apiErr.HTTPStatusCode)
		w.Write([]byte(apiErr.Description))
	case fs.BadDigest:
		apiErr := getAPIError(ErrBadDigest)
		w.WriteHeader(apiErr.HTTPStatusCode)
		w.Write([]byte(apiErr.Description))
	case fs.IncompleteBody:
		apiErr := getAPIError(ErrIncompleteBody)
		w.WriteHeader(apiErr.HTTPStatusCode)
		w.Write([]byte(apiErr.Description))
	case fs.ObjectExistsAsPrefix:
		apiErr := getAPIError(ErrObjectExistsAsPrefix)
		w.WriteHeader(apiErr.HTTPStatusCode)
		w.Write([]byte(apiErr.Description))
	case fs.ObjectNotFound:
		apiErr := getAPIError(ErrNoSuchKey)
		w.WriteHeader(apiErr.HTTPStatusCode)
		w.Write([]byte(apiErr.Description))
	case fs.ObjectNameInvalid:
		apiErr := getAPIError(ErrNoSuchKey)
		w.WriteHeader(apiErr.HTTPStatusCode)
		w.Write([]byte(apiErr.Description))
	default:
		apiErr := getAPIError(ErrInternalError)
		w.WriteHeader(apiErr.HTTPStatusCode)
		w.Write([]byte(apiErr.Description))
	}
}