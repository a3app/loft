/* Catch NSException from AppKit text layout. Swift cannot @catch it. */

#import <Foundation/Foundation.h>
#include <stdbool.h>

/// Runs `block`. Returns false if an NSException escaped.
bool LoftCatch(void (^block)(void));
