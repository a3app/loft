/* loft://, sstp://, vless://, trojan:// and one-link-per-line pastes. */
#ifndef LOFT_SHARE_H
#define LOFT_SHARE_H

#include "profile.h"

int loft_parse_share(const char *text, const char *file_name, LoftParsed *out, int max);

#endif
