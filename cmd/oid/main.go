package main

import (
	"fmt"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const templateStr = `
{
    "_id": { "$oid": "%s" },
    "stats_show": 0,
    "name": "",
    "activity": "",
    "description": "",
    "slogan": "",
    "address_line_1": "",
    "address_line_2": "",
    "city": "",
    "postal_code": "",
    "po_box": "",
    "country": "",
    "main_phone": "",
    "alt_phone": "",
    "mobile_phone": "",
    "email": "",
    "twitter": "",
    "facebook": "",
    "linkedin": "",
    "instagram": "",
    "tiktok": "",
    "whatsapp":"",
    "youtube": "",
    "website": "",
    "published": true,
    "name_seo": "",
    "geo": null,
    "updated_at": { "$date": "%s" },
    "created_at": { "$date": "%s" }
  }
`

func main() {
	oid := primitive.NewObjectID().Hex()
	created_at := time.Now().Format("2006-01-02T15:04:05.000Z")
	fmt.Fprintf(os.Stdout, templateStr, oid, created_at, created_at)
}
