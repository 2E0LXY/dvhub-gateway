/* Golden-vector generator against mbelib (ISC). Not part of the runtime.
 *   refdump golay           -> golayMatrix, one value per line
 *   refdump fec N           -> "<mode> <hex9> <49 bits>" for random frames
 *   refdump parms MODE N    -> per frame: "<49 bits> kind w0 L gamma vuv... | log2Ml..."
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "mbelib.h"
#define _MAIN
#include "dstar_const.h"
#include "dmr_const.h"

extern const int golayMatrix[2048];

static unsigned long long s = 88172645463325252ULL;
static unsigned rnd(void){ s ^= s<<13; s ^= s>>7; s ^= s<<17; return (unsigned)(s>>11); }

static int bit(const unsigned char*b,int i,int lsb){return lsb? (b[i/8]>>(i%8))&1 : (b[i/8]>>(7-i%8))&1;}

static void fec(int n){
  for(int k=0;k<n;k++) for(int m=0;m<2;m++){
    unsigned char f[9]; char fr[4][24], d[49]; memset(fr,0,sizeof fr);
    for(int i=0;i<9;i++) f[i]=rnd()&0xff;
    if(m==0){ for(int i=0;i<72;i++) fr[dW[i]][dX[i]]=bit(f,i,1);
      mbe_eccAmbe3600x2400C0(fr); mbe_demodulateAmbe3600x2400Data(fr); mbe_eccAmbe3600x2400Data(fr,d);}
    else { for(int i=0;i<36;i++){fr[rW[i]][rX[i]]=bit(f,2*i,0); fr[rY[i]][rZ[i]]=bit(f,2*i+1,0);}
      mbe_eccAmbe3600x2450C0(fr); mbe_demodulateAmbe3600x2450Data(fr); mbe_eccAmbe3600x2450Data(fr,d);}
    printf("%s ", m==0?"dstar":"dmr"); for(int i=0;i<9;i++) printf("%02x",f[i]); printf(" ");
    for(int i=0;i<49;i++) printf("%d",d[i]); printf("\n");
  }
}

static void parms(int dmr,int n){
  mbe_parms cur, prev, enh; mbe_initMbeParms(&cur,&prev,&enh);
  for(int k=0;k<n;k++){
    char d[49]; for(int i=0;i<49;i++) d[i]=rnd()&1;
    int bad = dmr ? mbe_decodeAmbe2450Parms(d,&cur,&prev) : mbe_decodeAmbe2400Parms(d,&cur,&prev);
    for(int i=0;i<49;i++) printf("%d",d[i]);
    printf(" %d %.7g %d %.7g ", bad, cur.w0, cur.L, cur.gamma);
    for(int l=1;l<=cur.L;l++) printf("%d",cur.Vl[l]);
    printf(" |");
    for(int l=1;l<=cur.L;l++) printf(" %.7g",cur.log2Ml[l]);
    printf("\n");
    if(bad==0) mbe_moveMbeParms(&cur,&prev);
  }
}

int main(int c,char**v){
  if(c>1 && !strcmp(v[1],"golay")){ for(int i=0;i<2048;i++) printf("%d\n",golayMatrix[i]); return 0;}
  if(c>2 && !strcmp(v[1],"fec")){ fec(atoi(v[2])); return 0;}
  if(c>3 && !strcmp(v[1],"parms")){ parms(!strcmp(v[2],"dmr"), atoi(v[3])); return 0;}
  fprintf(stderr,"usage\n"); return 1;
}
