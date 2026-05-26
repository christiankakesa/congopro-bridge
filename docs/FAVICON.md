# Favicon generation

```bash
mkdir -p ./images

convert -density 512 "/mnt/c/Users/chris/Downloads/Congopro Bridge - Logo - bg red.svg" \
-background none \
-alpha on \
-resize 192x192 \
./images/android-chrome-192x192.png

convert -density 512 "/mnt/c/Users/chris/Downloads/Congopro Bridge - Logo - bg red.svg" \
-background none \
-alpha on \
-resize 512x512 \
./images/android-chrome-512x512.png

convert -density 512 "/mnt/c/Users/chris/Downloads/Congopro Bridge - Logo - bg red.svg" \
-background none \
-alpha on \
-resize 180x180 \
./images/apple-touch-icon.png

convert -density 512 "/mnt/c/Users/chris/Downloads/Congopro Bridge - Logo - bg red.svg" \
-background none \
-alpha on \
-resize 150x150 \
./images/mstile-150x150.png

convert -density 512 "/mnt/c/Users/chris/Downloads/Congopro Bridge - Logo - bg red.svg" \
-background none \
-alpha on \
-resize 16x16 \
./images/favicon-16x16.png

convert -density 512 "/mnt/c/Users/chris/Downloads/Congopro Bridge - Logo - bg red.svg" \
-background none \
-alpha on \
-resize 32x32 \
./images/favicon-32x32.png

convert -density 512 "/mnt/c/Users/chris/Downloads/Congopro Bridge - Logo Text - Horizontal.svg" \
-background white \
-gravity center \
-resize 1200x630 \
-extent 1200x630 \
-quality 92 \
./images/og-image.jpg

convert -density 512 "/mnt/c/Users/chris/Downloads/Congopro Bridge - Logo Text - Horizontal.svg" \
-background white \
-gravity center \
-resize 1200x630 \
-extent 1200x630 \
-quality 92 \
./images/twitter-image.jpg

convert -density 512 "./favicon.svg" \
-background none \
-define icon:auto-resize=16,32,48,64,128,256 \
./../favicon.ico

install -D "/mnt/c/Users/chris/Downloads/Congopro Bridge - Logo - bg red.svg" ./favicon.svg
```
