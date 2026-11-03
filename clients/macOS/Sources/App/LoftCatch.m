#import "LoftCatch.h"

bool LoftCatch(void (^block)(void)) {
    if (block == NULL) {
        return true;
    }
    @try {
        block();
        return true;
    } @catch (NSException *e) {
        return false;
    }
}
