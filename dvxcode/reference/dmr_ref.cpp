#include "DMRFullLC.h"
#include "DMRLC.h"
#include "DMREmbeddedData.h"
#include "DMREMB.h"
#include "DMRSlotType.h"
#include "DMRDefines.h"
#include "DMRTA.h"
#include "Sync.h"
#include <cstdio>
#include <cstring>
#include <cstdlib>
static void hex(const unsigned char* d,int n){for(int i=0;i<n;i++)printf("%02x",d[i]);}
int main(){
  srand(1);
  for(int k=0;k<300;k++){
    unsigned src=rand()&0xFFFFFF, dst=rand()&0xFFFFFF;
    FLCO f = (k%2)?FLCO::GROUP:FLCO::USER_USER;
    CDMRLC lc(f,src,dst);
    lc.setFID(rand()&0xFF);
    unsigned char lcb[9]; lc.getData(lcb);
    unsigned char hdr[33]; memset(hdr,0,33); CDMRFullLC fl; fl.encode(lc,hdr,DT_VOICE_LC_HEADER);
    unsigned char term[33]; memset(term,0,33); fl.encode(lc,term,DT_TERMINATOR_WITH_LC);
    CDMREmbeddedData ed; ed.setLC(lc);
    printf("LC "); hex(lcb,9); printf(" HDR "); hex(hdr,33); printf(" TERM "); hex(term,33); printf(" EMBD");
    for(int n=1;n<=4;n++){unsigned char b[33]; memset(b,0,33); unsigned char lcss=ed.getData(b,n); printf(" %d:",lcss); hex(b+13,7);}
    printf("\n");
  }
  for(int cc=0;cc<16;cc++) for(int pi=0;pi<2;pi++) for(int lcss=0;lcss<4;lcss++){
    CDMREMB e; e.setColorCode(cc); e.setPI(pi); e.setLCSS(lcss); unsigned char b[33]; memset(b,0,33); e.getData(b);
    printf("EMB %d %d %d ",cc,pi,lcss); hex(b+13,7); printf("\n");
  }
  for(int cc=0;cc<16;cc++) for(int dt=0;dt<16;dt++){
    CDMRSlotType s; s.setColorCode(cc); s.setDataType(dt); unsigned char b[33]; memset(b,0,33); s.getData(b);
    printf("SLOT %d %d ",cc,dt); hex(b+12,9); printf("\n");
  }
}
