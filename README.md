# postcli

CLI tool for PoST initialization, this is a multi-threading version

# Getting it

Go to the https://github.com/fourierism/post/releases and take the most recent release for your platform. In case if you want to build it from source, follow the instructions below.

```bash
git clone https://github.com/fourierism/post.git
cd post
make postcli
```

# Usage

```bash
./postcli --help
```

###  Print the list of compute providers

```bash
./postcli -printProviders
```

###  Start with above providers id, raplace the xx in -id to your pubkey
```bash
./postcli  -datadir ./build/data  -numUnits 4 -provider="0,1" -id=xxx
```