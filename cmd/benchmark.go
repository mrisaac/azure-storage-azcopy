// Copyright © Microsoft <wastore@microsoft.com>
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package cmd

import (
	"errors"
	"fmt"
	"github.com/Azure/azure-storage-azcopy/azbfs"
	"github.com/Azure/azure-storage-azcopy/common"
	"github.com/Azure/azure-storage-blob-go/azblob"
	"github.com/Azure/azure-storage-file-go/azfile"
	"github.com/spf13/cobra"
	"net/url"
	"strconv"
	"strings"
)

// represents the raw benchmark command input from the user
type rawBenchmarkCmdArgs struct {
	// no src, since it's implicitly the auto-data-generator used for benchmarking

	// where are we uploading the benchmark data to?
	dst string

	// parameters controlling the auto-generated data
	sizePerFile string
	fileCount   uint

	// options from flags
	blockSizeMB  float64
	putMd5       bool
	blobType     string
	output       string
	logVerbosity string
}

const (
	maxBytesPerFile = 4.75 * 1024 * 1024 * 1024 * 1024

	// TODO would it be better to have a trailing B, eg. 12KB or 200GB? (that might make it case sensitive, or at least
	//    necessitate making the B case sensitive, because lowercase b means bits (and we don't want to bother supporting bits)
	sizeStringDescription = "a number immediately followed by K, M or G. E.g. 12k or 200G"

	sizePerFileParam = "size-per-file"
	fileCountParam   = "file-count"
)

// TODO move to copy handler util
func parseSizeString(s string, name string) (int64, error) {

	message := name + " must be " + sizeStringDescription

	if strings.Contains(s, " ") {
		return 0, errors.New(message)
	}
	if len(s) < 2 {
		return 0, errors.New(message)
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, errors.New(message)
	}
	suffix := strings.ToLower(s[len(s)-1:])

	bytes := int64(0)
	switch suffix {
	case "k":
		bytes = int64(n) * 1024
	case "m":
		bytes = int64(n) * 1024 * 1024
	case "g":
		bytes = int64(n) * 1024 * 1024 * 1024
	default:
		return 0, errors.New(message)
	}

	return bytes, nil
}

// validates and transform raw input into cooked input
// raw benchmark args cook into copyArgs, because the actual work
// of a benchmark job is doing a copy. Benchmark just doesn't offer so many
// choices in its raw args
func (raw rawBenchmarkCmdArgs) cook() (cookedCopyCmdArgs, error) {

	glcm.Info(common.BenchmarkPreviewNotice)

	dummyCooked := cookedCopyCmdArgs{}
	jobID := common.NewJobID()
	virtualDir := "benchmark-" + jobID.String() // create unique directory name, so we won't overwrite anything

	if raw.fileCount <= 0 {
		return dummyCooked, errors.New(fileCountParam + " must be greater than zero")
	}

	bytesPerFile, err := parseSizeString(raw.sizePerFile, sizePerFileParam)
	if err != nil {
		return dummyCooked, err
	}
	if bytesPerFile <= 0 {
		return dummyCooked, errors.New(sizePerFileParam + " must be greater than zero")
	}

	if bytesPerFile > maxBytesPerFile {
		return dummyCooked, errors.New("file size too big")
	}

	// transcribe everything to copy args
	c := rawCopyCmdArgs{}
	c.setMandatoryDefaults()

	// src must be string, but needs to indicate that its for benchmark and encode what we want
	c.src = benchmarkSourceHelper{}.ToUrl(raw.fileCount, bytesPerFile)

	c.dst, err = raw.appendVirtualDir(raw.dst, virtualDir)
	if err != nil {
		return dummyCooked, err
	}

	c.recursive = true  // because source is directory-like, in which case recursive is required
	c.forceWrite = true // don't want the extra round trip (for overwrite check) when benchmarking

	c.blockSizeMB = raw.blockSizeMB
	c.putMd5 = raw.putMd5
	c.blobType = raw.blobType
	c.output = raw.output
	c.logVerbosity = raw.logVerbosity

	cooked, err := c.cookWithId(jobID)
	if err != nil {
		return cooked, err
	}

	// set up automatic cleanup
	cooked.followupJobArgs, err = raw.createCleanupJobArgs(cooked.destination, raw.logVerbosity)
	if err != nil {
		return dummyCooked, err
	}

	return cooked, nil
}

func (raw rawBenchmarkCmdArgs) appendVirtualDir(target, virtualDir string) (string, error) {
	u, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("error parsing the url %s. Failed with error %s", u.String(), err.Error())
	}

	var result url.URL

	switch inferArgumentLocation(target) {
	case common.ELocation.Blob():
		p := azblob.NewBlobURLParts(*u)
		if p.ContainerName == "" || p.BlobName != "" {
			return "", errors.New("the blob target must be a container")
		}
		p.BlobName = virtualDir + "/"
		result = p.URL()

	case common.ELocation.File():
		p := azfile.NewFileURLParts(*u)
		if p.ShareName == "" || p.DirectoryOrFilePath != "" {
			return "", errors.New("the Azure Files target must be a file share root")
		}
		p.DirectoryOrFilePath = virtualDir + "/"
		result = p.URL()

	case common.ELocation.BlobFS():
		p := azbfs.NewBfsURLParts(*u)
		if p.FileSystemName == "" || p.DirectoryOrFilePath != "" {
			return "", errors.New("the blobFS target must be a file system")
		}
		p.DirectoryOrFilePath = virtualDir + "/"
		result = p.URL()
	default:
		return "", errors.New("benchmarking only supports https connections to Blob, Azure Files, and ADLSGen2")
	}

	return result.String(), nil
}

// define a cleanup job
func (raw rawBenchmarkCmdArgs) createCleanupJobArgs(benchmarkDest, logVerbosity string) (*cookedCopyCmdArgs, error) {

	rc := rawCopyCmdArgs{}

	rc.src = benchmarkDest // the SOURCE for the deletion is the the dest from the benchmark
	rc.recursive = true
	rc.logVerbosity = logVerbosity

	switch inferArgumentLocation(benchmarkDest) {
	case common.ELocation.Blob():
		rc.fromTo = common.EFromTo.BlobTrash().String()
	case common.ELocation.File():
		rc.fromTo = common.EFromTo.FileTrash().String()
	case common.ELocation.BlobFS():
		rc.fromTo = common.EFromTo.BlobFSTrash().String()
	default:
		return nil, errors.New("unsupported from-to for cleanup") // should never make it this far, due to earlier validation
	}

	rc.setMandatoryDefaults()

	cooked, err := rc.cook()
	cooked.isCleanupJob = true
	cooked.cleanupJobMessage = "Running cleanup job to delete files created during benchmarking"
	return &cooked, err
}

type benchmarkSourceHelper struct{}

// our code requires sources to be strings. So we may as well do the benchmark sources as URLs
// so we can identify then as such using a specific domain. ".invalid" is reserved globally for cases where
// you want a URL that can't possibly be a real one, so we'll use that
const benchmarkSourceHost = "benchmark.invalid"

func (h benchmarkSourceHelper) ToUrl(fileCount uint, bytesPerFile int64) string {
	result := fmt.Sprintf("https://%s/*?fc=%d&bpf=%d", benchmarkSourceHost, fileCount, bytesPerFile)
	if !pathPointsToContents(result) {
		panic("Need to update this when pathPointsToContents is changed or removed, since we need to prevent the processor func from trying to append part of this fake URL to the destination")
	}
	return result
}

func (h benchmarkSourceHelper) FromUrl(s string) (fileCount uint, bytesPerFile int64, err error) {
	// TODO: consider replace with regex?

	expectedPrefix := "https://" + benchmarkSourceHost + "/*?"
	if !strings.HasPrefix(s, expectedPrefix) {
		return 0, 0, errors.New("invalid benchmark source string")
	}
	s = strings.TrimPrefix(s, expectedPrefix)
	pieces := strings.Split(s, "&")
	if len(pieces) != 2 ||
		!strings.HasPrefix(pieces[0], "fc=") ||
		!strings.HasPrefix(pieces[1], "bpf=") {
		return 0, 0, errors.New("invalid benchmark source string")
	}
	pieces[0] = strings.Split(pieces[0], "=")[1]
	pieces[1] = strings.Split(pieces[1], "=")[1]
	fc, err := strconv.ParseUint(pieces[0], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	bpf, err := strconv.ParseInt(pieces[1], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	return uint(fc), bpf, nil
}

var benchCmd *cobra.Command

func init() {
	raw := rawBenchmarkCmdArgs{}

	// benCmd represents the bench command
	benchCmd = &cobra.Command{
		Use:        "bench [destination]",
		Aliases:    []string{"ben", "benchmark"},
		SuggestFor: []string{"b", "bn"},
		Short:      benchCmdShortDescription,
		Long:       benchCmdLongDescription,
		Example:    benchCmdExample,
		Args: func(cmd *cobra.Command, args []string) error {

			// TODO: if/when we support benchmarking for S2S, note that the current code to set userAgent string in
			//   jobPartMgr will need to be changed if we want it to still set the benchmarking suffix for S2S
			if len(args) == 1 {
				raw.dst = args[0]
			} else {
				return errors.New("wrong number of arguments, please refer to the help page on usage of this command")
			}
			return nil
		},
		Run: func(cmd *cobra.Command, args []string) {
			var cooked cookedCopyCmdArgs // benchmark args cook into copy args
			cooked, err := raw.cook()
			if err != nil {
				glcm.Error("failed to parse user input due to error: " + err.Error())
			}

			glcm.Info("Scanning...")

			cooked.commandString = copyHandlerUtil{}.ConstructCommandStringFromArgs()
			err = cooked.process()
			if err != nil {
				glcm.Error("failed to perform benchmark command due to error: " + err.Error())
			}

			glcm.SurrenderControl()
		},
	}
	rootCmd.AddCommand(benchCmd)

	benchCmd.PersistentFlags().StringVar(&raw.sizePerFile, sizePerFileParam, "250M", "size of each auto-generated data file. Must be "+sizeStringDescription)
	benchCmd.PersistentFlags().UintVar(&raw.fileCount, fileCountParam, 75, "number of auto-generated data files to use")
	_ = benchCmd.MarkFlagRequired(sizePerFileParam)
	_ = benchCmd.MarkFlagRequired(fileCountParam)

	benchCmd.PersistentFlags().Float64Var(&raw.blockSizeMB, "block-size-mb", 0, "use this block size (specified in MiB). Default is automatically calculated based on file size. Decimal fractions are allowed - e.g. 0.25. Identical to the same-named parameter in the copy command")
	benchCmd.PersistentFlags().StringVar(&raw.blobType, "blob-type", "None", "defines the type of blob at the destination. Used to allow benchmarking different blob types. Identical to the same-named parameter in the copy command")
	benchCmd.PersistentFlags().BoolVar(&raw.putMd5, "put-md5", false, "create an MD5 hash of each file, and save the hash as the Content-MD5 property of the destination blob/file. (By default the hash is NOT created.) Identical to the same-named parameter in the copy command")
	// TODO use constant for default value or, better, move loglevel param to root cmd?
	benchCmd.PersistentFlags().StringVar(&raw.logVerbosity, "log-level", "INFO", "define the log verbosity for the log file, available levels: INFO(all requests/responses), WARNING(slow responses), ERROR(only failed requests), and NONE(no output logs).")

}