#import <Mobile/Mobile.h>

/// gomobile emits both @protocol MobilePacketSink and @class MobilePacketSink.
/// Swift binds the class; this is the protocol the provider implements.
@protocol LoftPacketSink <MobilePacketSink>
@end

void LoftEngineSetSink(MobileEngine *_Nonnull eng, id<MobilePacketSink> _Nullable sink);
