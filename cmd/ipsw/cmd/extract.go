/*
Copyright © 2018-2022 blacktop

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/
package cmd

import (
	"archive/zip"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/apex/log"
	"github.com/blacktop/ipsw/internal/download"
	"github.com/blacktop/ipsw/internal/utils"
	"github.com/blacktop/ipsw/pkg/devicetree"
	"github.com/blacktop/ipsw/pkg/dyld"
	"github.com/blacktop/ipsw/pkg/info"
	"github.com/blacktop/ipsw/pkg/kernelcache"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(extractCmd)

	extractCmd.Flags().String("proxy", "", "HTTP/HTTPS proxy")
	extractCmd.Flags().Bool("insecure", false, "do not verify ssl certs")

	extractCmd.Flags().BoolP("remote", "r", false, "Extract from URL")
	extractCmd.Flags().BoolP("kernel", "k", false, "Extract kernelcache")
	extractCmd.Flags().BoolP("dyld", "d", false, "Extract dyld_shared_cache")
	extractCmd.Flags().BoolP("dtree", "t", false, "Extract DeviceTree")
	extractCmd.Flags().BoolP("dmg", "f", false, "Extract File System DMG")
	extractCmd.Flags().BoolP("iboot", "i", false, "Extract iBoot")
	extractCmd.Flags().BoolP("sep", "s", false, "Extract sep-firmware")
	extractCmd.Flags().String("pattern", "", "Download remote files that match (not regex)")
	extractCmd.Flags().StringP("output", "o", "", "Folder to extract files to")
	extractCmd.Flags().StringArrayP("dyld-arch", "a", []string{}, "dyld_shared_cache architecture to extract")

	extractCmd.MarkZshCompPositionalArgumentFile(1, "*.ipsw")
	extractCmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"ipsw", "zip"}, cobra.ShellCompDirectiveFilterFileExt
	}
}

func isURL(str string) bool {
	u, err := url.Parse(str)
	return err == nil && u.Scheme != "" && u.Host != ""
}

// extractCmd represents the extract command
var extractCmd = &cobra.Command{
	Use:           "extract <IPSW/OTA | URL>",
	Short:         "Extract kernelcache, dyld_shared_cache or DeviceTree from IPSW/OTA",
	Args:          cobra.MinimumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {

		if Verbose {
			log.SetLevel(log.DebugLevel)
		}

		kernelFlag, _ := cmd.Flags().GetBool("kernel")
		dyldFlag, _ := cmd.Flags().GetBool("dyld")
		deviceTreeFlag, _ := cmd.Flags().GetBool("dtree")
		dmgFlag, _ := cmd.Flags().GetBool("dmg")
		ibootFlag, _ := cmd.Flags().GetBool("iboot")
		sepFlag, _ := cmd.Flags().GetBool("sep")
		remote, _ := cmd.Flags().GetBool("remote")
		pattern, _ := cmd.Flags().GetString("pattern")
		output, _ := cmd.Flags().GetString("output")
		dyldArches, _ := cmd.Flags().GetStringArray("dyld-arch")

		if len(dyldArches) > 0 && !dyldFlag {
			return errors.New("--dyld-arch or -a can only be used with --dyld or -d")
		}
		if len(dyldArches) > 0 {
			for _, arch := range dyldArches {
				if !utils.StrSliceHas([]string{"arm64", "arm64e", "x86_64", "x86_64h"}, arch) {
					return fmt.Errorf("invalid dyld_shared_cache architecture '%s' (must be: arm64, arm64e, x86_64 or x86_64h)", arch)
				}
			}
		}

		destPath := filepath.Clean(output)

		if remote {
			proxy, _ := cmd.Flags().GetString("proxy")
			insecure, _ := cmd.Flags().GetBool("insecure")

			remoteURL := args[0]

			if !isURL(remoteURL) {
				log.Fatal("must supply valid URL when using the remote flag")
			}

			if dyldFlag {
				log.Error("unable to extract dyld_shared_cache remotely (try `ipsw download ota --dyld`)")
			}
			if dmgFlag {
				log.Error("unable to extract File System DMG remotely (let the author know if this is something you want)")
			}

			// Get handle to remote ipsw zip
			zr, err := download.NewRemoteZipReader(remoteURL, &download.RemoteConfig{
				Proxy:    proxy,
				Insecure: insecure,
			})
			if err != nil {
				return errors.Wrap(err, "failed to download kernelcaches from remote ipsw")
			}

			if kernelFlag {
				log.Info("Extracting remote kernelcache(s)")
				err = kernelcache.RemoteParse(zr, destPath)
				if err != nil {
					return errors.Wrap(err, "failed to download kernelcaches from remote ipsw")
				}
			}

			if deviceTreeFlag {
				log.Info("Extracting remote DeviceTree(s)")
				if err := utils.RemoteUnzip(zr.File, regexp.MustCompile(`.*DeviceTree.*im(3|4)p$`), destPath); err != nil {
					return fmt.Errorf("failed to extract DeviceTree: %v", err)
				}
			}

			if ibootFlag {
				log.Info("Extracting remote iBoot(s)")
				if err := utils.RemoteUnzip(zr.File, regexp.MustCompile(`.*iBoot.*im4p$`), destPath); err != nil {
					return fmt.Errorf("failed to extract iBoot: %v", err)
				}
			}

			if sepFlag {
				log.Info("Extracting sep-firmware(s)")
				if err := utils.RemoteUnzip(zr.File, regexp.MustCompile(`.*sep-firmware.*im4p$`), destPath); err != nil {
					return fmt.Errorf("failed to extract SEPOS: %v", err)
				}
			}

			if len(pattern) > 0 {
				log.Info("Extracting files matching pattern")
				validRegex, err := regexp.Compile(pattern)
				if err != nil {
					return errors.Wrap(err, "failed to compile regexp")
				}
				if err := utils.RemoteUnzip(zr.File, validRegex, destPath); err != nil {
					return fmt.Errorf("failed to extract files matching pattern: %v", err)
				}
			}
		} else {
			ipswPath := filepath.Clean(args[0])

			if _, err := os.Stat(ipswPath); os.IsNotExist(err) {
				return fmt.Errorf("file %s does not exist", ipswPath)
			}

			i, err := info.Parse(ipswPath)
			if err != nil {
				return errors.Wrap(err, "failed to parse ipsw info")
			}

			destPath = filepath.Join(destPath, i.GetFolder())

			if kernelFlag {
				log.Info("Extracting kernelcaches")
				err := kernelcache.Extract(ipswPath, destPath)
				if err != nil {
					return errors.Wrap(err, "failed to extract kernelcaches")
				}
			}

			if dyldFlag {
				log.Info("Extracting dyld_shared_cache")
				err := dyld.Extract(ipswPath, destPath, dyldArches)
				if err != nil {
					return errors.Wrap(err, "failed to extract dyld_shared_cache")
				}
			}

			if deviceTreeFlag {
				log.Info("Extracting DeviceTrees")
				err = devicetree.Extract(ipswPath, destPath)
				if err != nil {
					return errors.Wrap(err, "failed to extract DeviceTrees")
				}
			}

			if dmgFlag {
				log.Info("Extracting File System DMG")
				_, err = utils.Unzip(ipswPath, destPath, func(f *zip.File) bool {
					return strings.EqualFold(filepath.Base(f.Name), i.GetOsDmg())
				})
				if err != nil {
					return fmt.Errorf("failed extract %s from ipsw: %v", i.GetOsDmg(), err)
				}
				log.Infof("Created %s", filepath.Join(destPath, i.GetOsDmg()))
			}

			if ibootFlag {
				log.Info("Extracting iBoot")
				_, err := utils.Unzip(ipswPath, destPath, func(f *zip.File) bool {
					var validIBoot = regexp.MustCompile(`.*iBoot.*im4p$`)
					return validIBoot.MatchString(f.Name)
				})

				if err != nil {
					return errors.Wrap(err, "failed to extract iBoot from ipsw")
				}
			}

			if sepFlag {
				log.Info("Extracting sep-firmwares")
				_, err := utils.Unzip(ipswPath, destPath, func(f *zip.File) bool {
					var validSEP = regexp.MustCompile(`.*sep-firmware.*im4p$`)
					return validSEP.MatchString(f.Name)
				})

				if err != nil {
					return errors.Wrap(err, "failed to extract sep-firmware from ipsw")
				}
			}
		}

		return nil
	},
}
