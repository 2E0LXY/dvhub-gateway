#include "Utils.h"
void CUtils::byteToBitsBE(unsigned char byte, bool* bits){for(int i=0;i<8;i++)bits[i]=(byte>>(7-i))&1;}
void CUtils::bitsToByteBE(const bool* bits, unsigned char& byte){byte=0;for(int i=0;i<8;i++)byte=(byte<<1)|(bits[i]?1:0);}
