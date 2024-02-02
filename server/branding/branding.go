package branding

import (
	"fmt"
	"time"
)

var Name = "Cloud Fort"
var Copyright = fmt.Sprintf("Copyright © 2020-%d cloudfort, All Rights Reserved.", time.Now().Year())
var Banner = `    ___       ___   
   /\__\     /\  \  
  /:| _|_    \:\  \ 
 /::|/\__\   /::\__\
 \/|::/  /  /:/\/__/
   |:/  /   \/__/   
   \/__/            `
var Version = `v1.3.9`
var Hi = Banner + Version
