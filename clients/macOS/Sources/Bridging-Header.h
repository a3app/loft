/* Swift bridging header: imports C TUN (utun, userspace stack) and
   ChaCha20-Poly1305 AEAD into the loft macOS app. */

#include "Crypto/chacha20poly1305.h"
#include "TUN/utun.h"
#include "TUN/stack.h"
#include <CommonCrypto/CommonDigest.h>
#include <ifaddrs.h>
#include <net/if.h>
#include <netinet/in.h>
#include <netinet/tcp.h>
#include <arpa/inet.h>
