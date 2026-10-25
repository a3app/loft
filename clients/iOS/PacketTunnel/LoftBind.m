#import "LoftBind.h"

void LoftEngineSetSink(MobileEngine *eng, id<MobilePacketSink> sink) {
    [eng setSink:sink];
}
